//go:build integration

// Package integration runs several nylon nodes in one process over a simulated
// network.
//
// Tests run inside a testing/synctest bubble. Time in a bubble is virtual and
// only moves forward once every goroutine in it is blocked, so protocol timers,
// link latency and test timeouts take no wall-clock time, and a node that
// would hang forever is reported as a deadlock instead.
package integration

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"runtime/pprof"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide-wireguard-go/conn"
	"github.com/encodeous/polyamide-wireguard-go/conn/bindtest"
	"github.com/encodeous/polyamide-wireguard-go/device"
	"github.com/encodeous/polyamide-wireguard-go/tun"
	"github.com/encodeous/polyamide-wireguard-go/tun/tuntest"
)

func init() {
	// Nylon.Start calls signal.Notify, and the first Notify in a process
	// starts a goroutine that never exits. Started inside a bubble it would
	// belong to that bubble and keep it from finishing, so start it here.
	signal.Notify(make(chan os.Signal, 1), syscall.SIGINT, syscall.SIGTERM)
}

const (
	// startStagger spaces out node startup, like nodes coming up one by one.
	startStagger = 500 * time.Millisecond
	// pollInterval is how often Eventually re-checks its condition.
	pollInterval = 50 * time.Millisecond
	// dispatchTimeout bounds how long OnNode waits for a node's dispatcher.
	dispatchTimeout = 5 * time.Second
	// maxTestTime is the virtual time a test may run for. Running nodes keep
	// timers pending, so a test stuck on a wait with no timeout never looks
	// idle to synctest; the watchdog stops the nodes after this long so
	// synctest can report the deadlock. It is a few seconds of real time.
	maxTestTime   = 15 * time.Minute
	ipv4HeaderLen = 20
)

// Harness owns the nodes and the simulated network of one test.
//
// Build the topology with AddNode and Connect (or Link), adjust Central, Local
// or Tunables if needed, then call Start. The harness stops itself when the
// test ends.
type Harness struct {
	t *testing.T

	Central  state.CentralCfg
	Local    []state.LocalCfg
	Tunables *state.RouterTunables
	LogLevel slog.Level
	// UntrackedRouting turns off UseSystemRouting on every node, so packets
	// for peers stay inside polyamide instead of coming out of the TUN.
	UntrackedRouting bool

	ctx    context.Context
	cancel context.CancelCauseFunc
	ready  chan struct{} // closed once every node is constructed
	nodes  []*node
	wg     sync.WaitGroup // harness goroutines: senders, links, binds, TUNs
	stop   sync.Once

	// epOwner and firstEp are written by AddNode and only read after Start.
	epOwner map[netip.AddrPort]state.NodeId
	firstEp map[state.NodeId]netip.AddrPort

	mu    sync.RWMutex // guards links, binds and tuns
	links map[linkKey]*Link
	binds []conn.Bind
	tuns  []*tuntest.ChannelTUN

	onDeliver atomic.Pointer[PacketFilter]
	onTransit atomic.Pointer[PacketFilter]

	rngMu sync.Mutex
	rng   *rand.Rand
}

type node struct {
	nylon atomic.Pointer[core.Nylon]
	done  chan struct{} // closed when the node's Start returns
}

// NewHarness creates an empty harness that stops itself when t ends.
func NewHarness(t *testing.T) *Harness {
	seed := rand.Uint64()
	t.Logf("harness seed: %d", seed)
	h := &Harness{
		t:        t,
		LogLevel: slog.LevelDebug,
		epOwner:  make(map[netip.AddrPort]state.NodeId),
		firstEp:  make(map[state.NodeId]netip.AddrPort),
		links:    make(map[linkKey]*Link),
		rng:      rand.New(rand.NewPCG(seed, seed)),
	}
	t.Cleanup(h.Stop)
	return h
}

// AddNode adds a router that advertises prefix and is reachable at endpoints.
// Its first endpoint is also the source address of every packet it sends.
func (h *Harness) AddNode(id state.NodeId, prefix string, endpoints ...string) {
	key := state.GenerateKey()
	router := state.RouterCfg{
		NodeCfg: state.NodeCfg{
			Id:     id,
			PubKey: key.Pubkey(),
			Prefixes: []state.PrefixHealthWrapper{
				{PrefixHealth: &state.StaticPrefixHealth{Prefix: netip.MustParsePrefix(prefix)}},
			},
		},
		Endpoints: endpoints,
	}
	for i, ep := range endpoints {
		addr := netip.MustParseAddrPort(ep)
		h.epOwner[addr] = id
		if i == 0 {
			h.firstEp[id] = addr
		}
	}
	h.Central.Routers = append(h.Central.Routers, router)
	h.Local = append(h.Local, state.LocalCfg{
		Key:            key,
		Id:             id,
		Port:           25565,
		NoNetConfigure: true,
	})
}

func (h *Harness) indexOf(id state.NodeId) int {
	idx := slices.IndexFunc(h.Central.Routers, func(cfg state.RouterCfg) bool { return cfg.Id == id })
	if idx < 0 {
		h.t.Fatalf("unknown node %s", id)
	}
	return idx
}

// Router returns id's entry in the central config, for changes before Start.
func (h *Harness) Router(id state.NodeId) *state.RouterCfg {
	return &h.Central.Routers[h.indexOf(id)]
}

// LocalCfg returns id's local config, for changes before Start.
func (h *Harness) LocalCfg(id state.NodeId) *state.LocalCfg {
	return &h.Local[h.indexOf(id)]
}

// Nylon returns the running instance of node id.
func (h *Harness) Nylon(id state.NodeId) *core.Nylon {
	return h.nodes[h.indexOf(id)].nylon.Load()
}

// Start constructs every node, then starts them one startStagger apart.
func (h *Harness) Start() {
	h.t.Helper()
	h.ctx, h.cancel = context.WithCancelCause(context.Background())
	h.ready = make(chan struct{})
	h.nodes = make([]*node, len(h.Central.Routers))
	h.binds = make([]conn.Bind, len(h.Central.Routers))
	h.tuns = make([]*tuntest.ChannelTUN, len(h.Central.Routers))
	for idx := range h.nodes {
		h.nodes[idx] = &node{}
		h.Local[idx].UseSystemRouting = !h.UntrackedRouting
	}

	for idx := range h.nodes {
		n, err := h.newNylon(h.Central, h.Local[idx])
		if err != nil {
			// Nodes built so far own binds and TUNs, so tear them down.
			close(h.ready)
			h.Stop()
			h.t.Fatalf("construct node %s: %v", h.Local[idx].Id, err)
		}
		h.nodes[idx].nylon.Store(n)
	}
	close(h.ready)

	for idx, nd := range h.nodes {
		h.launch(nd, nd.nylon.Load(), time.Duration(idx)*startStagger)
	}
	h.spawn(h.watchdog)
}

func (h *Harness) watchdog() {
	select {
	case <-h.ctx.Done():
	case <-time.After(maxTestTime):
		h.fail("test still running after %s of virtual time, stopping nodes so synctest can report what is blocked", maxTestTime)
		for _, nd := range h.nodes {
			if n := nd.nylon.Load(); n != nil {
				n.Stop()
			}
		}
	}
}

func (h *Harness) newNylon(central state.CentralCfg, local state.LocalCfg) (*core.Nylon, error) {
	return core.NewNylon(central, local, h.LogLevel, "", map[string]any{"vnet": h},
		state.NylonOptions{DBG_log_wireguard: true}, h.Tunables)
}

// launch runs n.Start in the background after delay.
func (h *Harness) launch(nd *node, n *core.Nylon, delay time.Duration) {
	nd.done = make(chan struct{})
	done := nd.done
	go func() {
		defer close(done)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-h.ctx.Done():
			// Start anyway: it returns at once on a stopped node, and its
			// exit path releases what NewNylon allocated.
			timer.Stop()
		}
		pprof.Do(context.Background(), pprof.Labels("nylon node", string(n.LocalCfg.Id)), func(context.Context) {
			if err := n.Start(); err != nil {
				h.fail("node %s: %v", n.LocalCfg.Id, err)
			}
		})
	}()
}

// RestartNode stops node id and starts a fresh instance from central and the
// node's local config after mutate (if non-nil) has edited it. The rest of the
// network keeps running.
func (h *Harness) RestartNode(id state.NodeId, central state.CentralCfg, mutate func(*state.LocalCfg)) {
	h.t.Helper()
	idx := h.indexOf(id)
	nd := h.nodes[idx]
	nd.nylon.Load().Stop()
	await(h, nd.done, dispatchTimeout, "node "+string(id)+" to stop")

	h.mu.Lock()
	old := h.binds[idx]
	h.binds[idx] = nil
	h.mu.Unlock()
	if err := old.Close(); err != nil {
		h.t.Fatalf("close bind of %s: %v", id, err)
	}

	if mutate != nil {
		mutate(&h.Local[idx])
	}
	n, err := h.newNylon(central, h.Local[idx])
	if err != nil {
		h.t.Fatalf("reconstruct node %s: %v", id, err)
	}
	nd.nylon.Store(n)
	h.launch(nd, n, 0)
}

// Stop shuts every node and harness goroutine down. It runs on test cleanup
// and is safe to call more than once.
func (h *Harness) Stop() {
	h.stop.Do(func() {
		if h.cancel == nil {
			return // never started
		}
		h.cancel(errors.New("harness stopped"))
		for _, nd := range h.nodes {
			if n := nd.nylon.Load(); n != nil {
				n.Stop()
			}
		}
		for _, nd := range h.nodes {
			if nd.done != nil {
				<-nd.done
			}
		}
		// Start's exit path cleans up a node, but a node that was built and
		// never launched (Start failing partway) needs it here. Cleanup is
		// idempotent.
		for _, nd := range h.nodes {
			if n := nd.nylon.Load(); n != nil {
				_ = n.Cleanup()
			}
		}
		h.mu.Lock()
		for _, b := range h.binds {
			if b != nil {
				_ = b.Close()
			}
		}
		h.mu.Unlock()
		h.wg.Wait()
	})
}

// fail reports a harness error and stops waits in progress. It is safe to
// call from any goroutine the harness owns.
func (h *Harness) fail(format string, args ...any) {
	err := fmt.Errorf(format, args...)
	h.t.Errorf("harness: %v", err)
	h.cancel(err)
}

// spawn runs fn on a goroutine that Stop waits for.
func (h *Harness) spawn(fn func()) {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		fn()
	}()
}

// ---- waiting ----

// Eventually polls cond until it returns true, failing the test after timeout.
func (h *Harness) Eventually(timeout time.Duration, what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		select {
		case <-h.ctx.Done():
			h.t.Fatalf("harness stopped while waiting for %s: %v", what, context.Cause(h.ctx))
		case <-time.After(pollInterval):
		}
	}
}

// await receives from ch, failing the test after timeout.
func await[T any](h *Harness, ch <-chan T, timeout time.Duration, what string) T {
	h.t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		h.t.Fatalf("timed out after %s waiting for %s", timeout, what)
	case <-h.ctx.Done():
		h.t.Fatalf("harness stopped while waiting for %s: %v", what, context.Cause(h.ctx))
	}
	panic("unreachable")
}

// OnNode runs fn on node id's dispatcher and returns its result. The
// dispatcher owns router state, so fn may read and change it freely.
func OnNode[T any](h *Harness, id state.NodeId, fn func(n *core.Nylon) T) T {
	h.t.Helper()
	n := h.Nylon(id)
	res := make(chan T, 1)
	n.Dispatch(func() error {
		res <- fn(n)
		return nil
	})
	return await(h, res, dispatchTimeout, "dispatch on node "+string(id))
}

// ApplyConfig applies cfg on node id and fails the test unless it applied.
func (h *Harness) ApplyConfig(id state.NodeId, cfg *state.CentralCfg) {
	h.t.Helper()
	type result struct {
		res core.ApplyResult
		err error
	}
	r := OnNode(h, id, func(n *core.Nylon) result {
		res, err := n.ApplyCentralConfig(cfg)
		return result{res, err}
	})
	if r.err != nil {
		h.t.Fatalf("apply config on %s: %v", id, r.err)
	}
	if r.res != core.ApplyApplied {
		h.t.Fatalf("apply config on %s: got result %v, want applied", id, r.res)
	}
}

// ---- packets ----

// PacketFilter sees an IPv4 packet at node. Returning true consumes it.
type PacketFilter func(node state.NodeId, src, dst netip.Addr, data []byte) bool

// OnDeliver sets the filter for packets that reach the node they are addressed
// to. Without one, delivered packets are dropped.
func (h *Harness) OnDeliver(f PacketFilter) { h.onDeliver.Store(&f) }

// OnTransit sets the filter for packets a node forwards on to another node.
// Packets it does not consume are routed onwards as usual.
func (h *Harness) OnTransit(f PacketFilter) { h.onTransit.Store(&f) }

func applyFilter(p *atomic.Pointer[PacketFilter], node state.NodeId, src, dst netip.Addr, data []byte) bool {
	f := p.Load()
	return f != nil && (*f)(node, src, dst, data)
}

// Send injects an IPv4 packet carrying payload into node's TUN, as if an
// application on that node had sent it.
func (h *Harness) Send(node state.NodeId, src, dst string, payload []byte, ttl byte) {
	pkt := make([]byte, ipv4HeaderLen, ipv4HeaderLen+len(payload))
	pkt[0] = 4<<4 | ipv4HeaderLen/4
	binary.BigEndian.PutUint16(pkt[2:4], uint16(ipv4HeaderLen+len(payload)))
	pkt[8] = ttl
	copy(pkt[12:16], netip.MustParseAddr(src).AsSlice())
	copy(pkt[16:20], netip.MustParseAddr(dst).AsSlice())
	pkt = append(pkt, payload...)

	h.mu.RLock()
	tn := h.tuns[h.indexOf(node)]
	h.mu.RUnlock()
	select {
	case tn.Outbound <- pkt:
	case <-h.ctx.Done():
	}
}

// SendEvery sends the packet every interval until the harness stops or the
// returned function is called.
func (h *Harness) SendEvery(interval time.Duration, node state.NodeId, src, dst string, payload []byte, ttl byte) (stop func()) {
	ctx, cancel := context.WithCancel(h.ctx)
	h.spawn(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.Send(node, src, dst, payload, ttl)
			}
		}
	})
	return cancel
}

// ---- links ----

type linkKey struct{ from, to netip.AddrPort }

// Link is a one-way path between two endpoints. Packets on endpoint pairs
// without a link are dropped.
type Link struct {
	mu         sync.Mutex
	latency    time.Duration
	jitter     time.Duration
	packetLoss float64
}

// Link adds a one-way link from one endpoint to another.
func (h *Harness) Link(from, to string) *Link {
	l := &Link{}
	h.mu.Lock()
	h.links[linkKey{netip.MustParseAddrPort(from), netip.MustParseAddrPort(to)}] = l
	h.mu.Unlock()
	return l
}

// Links is a set of links configured together.
type Links []*Link

// Connect adds links in both directions between two endpoints.
func (h *Harness) Connect(a, b string) Links {
	return Links{h.Link(a, b), h.Link(b, a)}
}

// WithLatency delays each packet by lat plus up to jitter.
func (l *Link) WithLatency(lat, jitter time.Duration) *Link {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.latency, l.jitter = lat, jitter
	return l
}

// WithPacketLoss drops each packet with probability loss.
func (l *Link) WithPacketLoss(loss float64) *Link {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.packetLoss = loss
	return l
}

func (ls Links) WithLatency(lat, jitter time.Duration) Links {
	for _, l := range ls {
		l.WithLatency(lat, jitter)
	}
	return ls
}

func (ls Links) WithPacketLoss(loss float64) Links {
	for _, l := range ls {
		l.WithPacketLoss(loss)
	}
	return ls
}

func (h *Harness) random() float64 {
	h.rngMu.Lock()
	defer h.rngMu.Unlock()
	return h.rng.Float64()
}

// transmit carries a packet across the link from one endpoint to another.
func (h *Harness) transmit(pkt []byte, from, to netip.AddrPort) {
	h.mu.RLock()
	link := h.links[linkKey{from, to}]
	h.mu.RUnlock()
	if link == nil {
		return // no link, the packet is lost
	}
	link.mu.Lock()
	latency, jitter, loss := link.latency, link.jitter, link.packetLoss
	link.mu.Unlock()

	if loss > 0 && h.random() < loss {
		return
	}
	if latency == 0 && jitter == 0 {
		h.receive(pkt, from, to)
		return
	}
	delay := latency + time.Duration(h.random()*float64(jitter))
	h.spawn(func() {
		select {
		case <-h.ctx.Done():
		case <-time.After(delay):
			h.receive(pkt, from, to)
		}
	})
}

// receive hands a packet to the bind of the node that owns endpoint to.
func (h *Harness) receive(pkt []byte, from, to netip.AddrPort) {
	owner, ok := h.epOwner[to]
	if !ok {
		return
	}
	h.mu.RLock()
	b := h.binds[h.indexOf(owner)]
	h.mu.RUnlock()
	if b == nil {
		return // node is restarting
	}
	err := b.Send([][]byte{pkt}, bindtest.ChannelEndpoint2(from))
	if err != nil && !errors.Is(err, net.ErrClosed) {
		h.fail("deliver packet to %s: %v", owner, err)
	}
}

// ---- runtime for core ----

// NewRuntime gives a node a virtual TUN and a WireGuard transport whose UDP
// socket is attached to the simulated network. core calls it through the
// "vnet" aux config entry.
func (h *Harness) NewRuntime(n *core.Nylon) (core.Runtime, error) {
	id := n.LocalCfg.Id
	var host tun.Device
	if n.NoTun {
		host = tun.NewDummyDevice("nylon-vn")
	} else {
		host = h.newTun(id)
	}
	transport, err := core.NewWireGuardTransport(n, func() conn.Bind { return h.newBind(id) })
	if err != nil {
		_ = host.Close()
		return core.Runtime{}, err
	}
	return core.Runtime{Transports: []polyamide.Transport{transport}, Host: host}, nil
}

// newBind returns the node's end of a channel bind, and pumps packets the
// node sends from the other end onto the simulated network.
func (h *Harness) newBind(id state.NodeId) conn.Bind {
	pair := bindtest.NewChannelBind2()
	remote := pair[0]
	recvFns, _, err := remote.Open(0)
	if err != nil {
		h.fail("open bind for %s: %v", id, err)
		return nil
	}
	h.mu.Lock()
	h.binds[h.indexOf(id)] = remote
	h.mu.Unlock()
	from := h.firstEp[id]

	h.spawn(func() {
		select {
		case <-h.ready:
		case <-h.ctx.Done():
			return
		}
		batch := remote.BatchSize()
		bufs := make([][]byte, batch)
		bufs[0] = make([]byte, device.MaxMessageSize)
		sizes := make([]int, batch)
		eps := make([]conn.Endpoint, batch)
		for {
			for _, recv := range recvFns {
				n, err := recv(bufs, sizes, eps)
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						h.fail("receive from %s's bind: %v", id, err)
					}
					return
				}
				for i := range n {
					if sizes[i] == 0 {
						continue
					}
					to := netip.AddrPort(eps[i].(bindtest.ChannelEndpoint2))
					h.transmit(slices.Clone(bufs[i][:sizes[i]]), from, to)
				}
			}
		}
	})
	return pair[1]
}

// newTun returns a TUN for node id. Packets the node writes to it are either
// delivered locally or, when addressed to another node, sent back into the
// TUN so the node routes them like any outbound packet.
func (h *Harness) newTun(id state.NodeId) tun.Device {
	tn := tuntest.NewChannelTUN()
	h.mu.Lock()
	h.tuns[h.indexOf(id)] = tn
	h.mu.Unlock()

	h.spawn(func() {
		select {
		case <-h.ready:
		case <-h.ctx.Done():
			return
		}
		for {
			select {
			case <-h.ctx.Done():
				return
			case pkt := <-tn.Inbound:
				if pkt[0]>>4 != 4 {
					h.fail("node %s wrote a non-IPv4 packet to its TUN", id)
					return
				}
				src, _ := netip.AddrFromSlice(pkt[12:16])
				dst, _ := netip.AddrFromSlice(pkt[16:20])
				if !h.routeFromTun(id, tn, src, dst, pkt) {
					h.fail("node %s: no route for packet %v -> %v", id, src, dst)
					return
				}
			}
		}
	})
	return tn.TUN()
}

// routeFromTun stands in for the host's routing table.
func (h *Harness) routeFromTun(id state.NodeId, tn *tuntest.ChannelTUN, src, dst netip.Addr, pkt []byte) bool {
	data := pkt[ipv4HeaderLen:]
	// a packet whose TTL ran out is handed to the local stack as well
	if pkt[8] == 0 && applyFilter(&h.onDeliver, id, src, dst, data) {
		return true
	}
	for _, prefix := range h.Central.GetNode(id).Prefixes {
		if prefix.GetPrefix().Contains(dst) && applyFilter(&h.onDeliver, id, src, dst, data) {
			return true
		}
	}
	if applyFilter(&h.onTransit, id, src, dst, data) {
		return true
	}
	for _, router := range h.Central.Routers {
		if router.Id == id {
			continue
		}
		for _, prefix := range router.Prefixes {
			if prefix.GetPrefix().Contains(dst) {
				select {
				case tn.Outbound <- pkt:
				default:
					h.t.Logf("node %s: TUN outbound queue full, dropping packet", id)
				}
				return true
			}
		}
	}
	return false
}

// Signal is a one-shot event that can be triggered more than once.
type Signal chan struct{}

func NewSignal() Signal { return make(Signal) }

func (s Signal) Trigger() {
	select {
	case <-s:
	default:
		close(s)
	}
}

func (s Signal) Triggered() bool {
	select {
	case <-s:
		return true
	default:
		return false
	}
}
