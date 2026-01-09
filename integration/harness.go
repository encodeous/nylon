//go:build integration

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
	"runtime/pprof"
	"slices"
	"sync"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/conn/bindtest"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/polyamide/tun/tuntest"
	"github.com/encodeous/nylon/state"
)

type Signal chan bool

func NewSignal() Signal {
	return make(chan bool)
}
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
func (s Signal) Wait() {
	<-s
}

type VirtualLink struct {
	Edge       state.Pair[bindtest.ChannelEndpoint2, bindtest.ChannelEndpoint2]
	Latency    time.Duration
	Jitter     time.Duration
	PacketLoss float64
}

func (v *VirtualLink) simulate(pkt []byte, len int, from, to bindtest.ChannelEndpoint2, i *InMemoryNetwork) {
	//fmt.Printf("begin send: %s -> %s\n", from.DstToString(), to.DstToString())
	if rand.Float64() < v.PacketLoss {
		// drop
		//fmt.Printf("dropped send: %s -> %s\n", from.DstToString(), to.DstToString())
		return
	}

	toIdx := i.cfg.IndexOf(i.cfg.Endpoints[to.DstToString()])
	if v.Latency != 0 {
		simJitter := rand.Float64() * float64(v.Jitter.Nanoseconds())
		simLat := v.Latency + time.Duration(simJitter)
		go func() { // this is quite costly, but should be fine as long as we don't send packets too fast
			select {
			case <-i.cfg.Context.Done():
				return
			case <-time.After(simLat):
				//fmt.Printf("delay sent: %s -> %s\n", from.DstToString(), to.DstToString())
				err := i.binds[toIdx].Send([][]byte{pkt[:len]}, from)
				if err != nil && !errors.Is(err, net.ErrClosed) {
					panic(err)
				}
			}
		}()
	} else {
		//fmt.Printf("sent: %s -> %s\n", from.DstToString(), to.DstToString())
		err := i.binds[toIdx].Send([][]byte{pkt[:len]}, from)
		if err != nil && !errors.Is(err, net.ErrClosed) {
			panic(err)
		}
	}
}

func (v *VirtualLink) WithLatency(lat, jitter time.Duration) *VirtualLink {
	v.Latency = lat
	v.Jitter = jitter
	return v
}

func (v *VirtualLink) WithPacketLoss(loss float64) *VirtualLink {
	v.PacketLoss = loss
	return v
}

type VirtualHarness struct {
	Central          state.CentralCfg
	Context          context.Context
	Cancel           context.CancelCauseFunc
	Local            []state.LocalCfg
	Net              *InMemoryNetwork
	States           []*state.State
	Links            []*VirtualLink
	Endpoints        map[string]state.NodeId
	UntrackedRouting bool
}

func (v *VirtualHarness) IndexOf(id state.NodeId) int {
	return slices.IndexFunc(v.Central.Routers, func(cfg state.RouterCfg) bool {
		return cfg.Id == id
	})
}

func (v *VirtualHarness) NewNode(id state.NodeId, virtPrefix string) {
	privKey := state.GenerateKey()
	locCfg := state.LocalCfg{
		Key:              privKey,
		Id:               id,
		Port:             25565,
		NoNetConfigure:   true,
		UseSystemRouting: !v.UntrackedRouting,
	}
	ncfg := state.RouterCfg{
		NodeCfg: state.NodeCfg{
			Id:     id,
			PubKey: privKey.Pubkey(),
			Prefixes: []state.PrefixHealthWrapper{
				{
					&state.StaticPrefixHealth{
						Prefix: netip.MustParsePrefix(virtPrefix),
						Metric: 0,
					},
				},
			},
		},
	}
	v.Central.Routers = append(v.Central.Routers, ncfg)
	v.Local = append(v.Local, locCfg)
}

func (v *VirtualHarness) AddLink(from, to string) *VirtualLink {
	link := &VirtualLink{}
	link.Edge = state.Pair[bindtest.ChannelEndpoint2, bindtest.ChannelEndpoint2]{
		V1: bindtest.ChannelEndpoint2(netip.MustParseAddrPort(from)),
		V2: bindtest.ChannelEndpoint2(netip.MustParseAddrPort(to)),
	}
	v.Links = append(v.Links, link)
	return link
}

func (v *VirtualHarness) Start() chan error {
	ctx, cancel := context.WithCancelCause(context.Background())
	v.Context = ctx
	v.Cancel = cancel
	v.States = make([]*state.State, len(v.Central.Routers))
	errChan := make(chan error, 128) // a large number so we dont get blocked
	vn := &InMemoryNetwork{}
	v.Net = vn
	vn.cfg = v
	nodes := len(v.Central.Routers)
	vn.virtTun = make([]*tuntest.ChannelTUN, nodes)
	vn.binds = make([]conn.Bind, nodes)
	// pick the first endpoint specified for each node
	vn.EpOutMapping = func(curNode state.NodeId, to bindtest.ChannelEndpoint2) bindtest.ChannelEndpoint2 {
		for k, x := range v.Endpoints {
			if x == curNode {
				return bindtest.ChannelEndpoint2(netip.MustParseAddrPort(k))
			}
		}
		panic(fmt.Sprintf("no endpoint found for node %v", curNode))
	}
	for e, n := range v.Endpoints {
		idx := v.IndexOf(n)
		v.Central.Routers[idx].Endpoints = append(v.Central.Routers[idx].Endpoints, netip.MustParseAddrPort(e))
	}
	startDelay := 0 * time.Millisecond
	for idx, rt := range v.Central.Routers {
		sd := startDelay
		go func() {
			time.Sleep(sd)
			labels := pprof.Labels("nylon node", string(rt.Id))
			pprof.Do(context.Background(), labels, func(_ context.Context) {
				restart, cErr := core.Start(v.Central, v.Local[idx], slog.LevelDebug, "", map[string]any{
					"vnet": vn,
				}, &v.States[idx])
				if cErr != nil {
					errChan <- cErr
					return
				}
				if restart {
					panic(fmt.Sprintf("node restart is not implemented"))
				}
			})
		}()
		startDelay += time.Millisecond * 500 // add a tiny delay so they don't try to handshake at the exact same time
	}
	// wait for all routers to start
	for {
		started := true
		for idx, _ := range v.Central.Routers {
			if v.States[idx] == nil || !v.States[idx].Started.Load() {
				started = false
				break
			}
		}
		if started {
			break
		}
		select {
		case <-ctx.Done():
			return errChan
		case <-time.After(time.Millisecond * 50):
		case err := <-errChan:
			errChan <- err
			return errChan
		}
	}
	return errChan
}

func (v *VirtualHarness) Stop() {
	println("Stopping VirtualHarness")
	v.Cancel(fmt.Errorf("stopping harness"))
	for idx, _ := range v.Central.Routers {
		core.Stop(v.States[idx])
	}
	v.Net.Stop()
	println("Stopped VirtualHarness")
}

type PacketFilter func(node state.NodeId, src, dst netip.Addr, data []byte) bool // return true to intercept, and stop the rest of the stack
func (h PacketFilter) TryApply(node state.NodeId, src, dst netip.Addr, data []byte) bool {
	if h == nil {
		return false
	}
	return h(node, src, dst, data)
}

type OutMapping func(curNode state.NodeId, to bindtest.ChannelEndpoint2) bindtest.ChannelEndpoint2

type InMemoryNetwork struct {
	sync.Mutex
	cfg            *VirtualHarness
	binds          []conn.Bind
	virtTun        []*tuntest.ChannelTUN
	SelfHandler    PacketFilter // packet filter for handling packets destined for the current node
	TransitHandler PacketFilter // packet filter for handling packets passing through the current node
	EpOutMapping   OutMapping
}

func (i *InMemoryNetwork) virtualRouteTable(node state.NodeId, src, dst netip.Addr, data []byte, pkt []byte) bool {
	curCfg := i.cfg.Central.GetNode(node)
	if pkt[8] == 0 { // handle self if ttl is 0 as well
		if i.SelfHandler.TryApply(node, src, dst, data) {
			return true
		}
	}
	for _, prefix := range curCfg.Prefixes {
		if prefix.GetPrefix().Contains(dst) {
			if i.SelfHandler.TryApply(node, src, dst, data) {
				return true
			}
		}
	}
	curIdx := i.cfg.IndexOf(node)

	if !i.TransitHandler.TryApply(node, src, dst, data) {
		// default routing behaviour
		for _, n := range i.cfg.Central.Routers {
			if node == n.Id {
				continue
			}
			for _, prefix := range n.Prefixes {
				if prefix.GetPrefix().Contains(dst) {
					// route to this node
					select {
					case i.virtTun[curIdx].Outbound <- pkt: // send back into our tun to get routed by WireGuard/Polyamide
						return true
					default:
						fmt.Printf("%s's tun is not ready to accept data\n", n.Id)
						return true
					}
				}
			}
		}
		return false
	}
	return true
}

func (i *InMemoryNetwork) virtualInternet(pkt []byte, len int, from, to bindtest.ChannelEndpoint2) {
	// simulate network conditions
	idx := slices.IndexFunc(i.cfg.Links, func(link *VirtualLink) bool {
		return link.Edge.V1 == from && link.Edge.V2 == to
	})
	if idx == -1 {
		return // no connection, dropped packet
	}
	i.cfg.Links[idx].simulate(pkt, len, from, to, i)
}

func (i *InMemoryNetwork) Bind(node state.NodeId) conn.Bind {
	i.Lock()
	defer i.Unlock()
	epSendMapping := func(to bindtest.ChannelEndpoint2) bindtest.ChannelEndpoint2 {
		return i.EpOutMapping(node, to)
	}
	bp := bindtest.NewChannelBind2()

	remote := bp[0] // simulate an actual port opening
	open, _, err := remote.Open(0)
	if err != nil {
		return nil
	}
	numId := i.cfg.IndexOf(node)
	i.binds[numId] = remote
	go func() {
		// bind listener routine for packets sent from this node
		bufSize := remote.BatchSize()
		pktBuf := make([][]byte, bufSize)
		pktBuf[0] = make([]byte, device.MaxMessageSize)
		lenBuf := make([]int, bufSize)
		epBuf := make([]conn.Endpoint, bufSize)
		for {
			for _, recv := range open {
				n, err := recv(pktBuf, lenBuf, epBuf)
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						panic(err)
					}
					return
				}
				for pi := range n {
					if lenBuf[pi] == 0 {
						continue
					}
					toIp := epBuf[pi].(bindtest.ChannelEndpoint2)
					fromIp := epSendMapping(toIp)
					i.virtualInternet(slices.Clone(pktBuf[pi]), lenBuf[pi], fromIp, toIp)
				}
			}
		}
	}()
	return bp[1]
}

func (i *InMemoryNetwork) Tun(node state.NodeId) tun.Device {
	i.Lock()
	defer i.Unlock()
	const (
		ipv4Size = 20
	)
	bt := tuntest.NewChannelTUN()

	numId := slices.IndexFunc(i.cfg.Central.Routers, func(cfg state.RouterCfg) bool {
		return cfg.Id == node
	})

	i.virtTun[numId] = bt
	go func() {
		for {
			select {
			case <-i.cfg.Context.Done():
				return
			case pkt := <-bt.Inbound:
				// wireguard doesn't actually verify checksum for ip
				ip := pkt[0:ipv4Size]
				if pkt[0]>>4 != 4 {
					panic("unexpected packet, not ipv4")
				}
				var src, dst netip.Addr
				err := src.UnmarshalBinary(ip[12:16])
				if err != nil {
					panic(err)
				}
				err = dst.UnmarshalBinary(ip[16:20])
				if err != nil {
					panic(err)
				}
				if !i.virtualRouteTable(node, src, dst, pkt[ipv4Size:], pkt) {
					panic(fmt.Sprintf("unhandled packet src: %v, dst: %v", src, dst))
				}
			}
		}
	}()
	return bt.TUN()
}

func (i *InMemoryNetwork) Send(node state.NodeId, src, dst string, pkt []byte, ttl byte) {
	const (
		ipv4Size = 20
	)
	numId := i.cfg.IndexOf(node)
	ipPkt := append(make([]byte, ipv4Size), pkt...)
	ip := ipPkt[0:ipv4Size]
	ip[0] = 4 << 4
	binary.BigEndian.PutUint16(ip[2:4], uint16(ipv4Size+len(pkt)))
	ip[8] = ttl
	copy(ipPkt[12:16], netip.MustParseAddr(src).AsSlice())
	copy(ipPkt[16:20], netip.MustParseAddr(dst).AsSlice())
	i.virtTun[numId].Outbound <- ipPkt
}

func (i *InMemoryNetwork) Stop() {
	for _, d := range i.binds {
		if d != nil {
			err := d.Close()
			if err != nil {
				panic(err)
			}
		}
	}
}
