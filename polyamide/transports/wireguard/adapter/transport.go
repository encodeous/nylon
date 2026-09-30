// Package adapter implements Polyamide using wireguard-go.
package adapter

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/ecdh"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/device"
)

type Options struct {
	PrivateKey polyamide.PrivateKey
	ListenPort uint16
	// Nil uses the platform UDP bind.
	NewBind         func() conn.Bind
	Logger          *slog.Logger
	Debug           bool
	ControlHandlers map[string]func(*bufio.ReadWriter) error
}

type Transport struct {
	runContext  context.Context
	opts        Options
	bind        conn.Bind
	localPublic device.NoisePublicKey
	// ops protects configuration and device startup and shutdown. Callbacks do not lock it.
	ops                 sync.Mutex
	mu                  sync.RWMutex
	dev                 *device.Device
	peers               map[device.NoisePublicKey]*peer
	byRaw               map[*device.Peer]*peer
	configuredEndpoints map[*peer][]conn.Endpoint
	started             atomic.Bool
	closed              atomic.Bool
	hooks               atomic.Pointer[polyamide.Hooks]
	// Stop accepting packets before waiting for workers. Callbacks can send replies
	// without taking the configuration lock.
	submitMu    sync.Mutex
	submissions sync.WaitGroup
	done        chan struct{}
	stopContext func() bool
	batchPool   sync.Pool
	host        *hostSink
}

type peer struct {
	owner      *Transport
	id         string
	public     device.NoisePublicKey
	passive    bool         // protected by ops
	raw        *device.Peer // published under mu before startup. It does not change after startup.
	retired    atomic.Bool
	endpointMu sync.Mutex
	// learned is the last endpoint reported through Hooks.EndpointLearned.
	learned atomic.Pointer[netip.AddrPort]
}

func (p *peer) Transport() polyamide.Transport { return p.owner }
func (p *peer) ID() string                     { return p.id }
func (p *peer) PublicKey() polyamide.PublicKey { return polyamide.PublicKey(p.public) }

var _ polyamide.Transport = (*Transport)(nil)

func New(opts Options) (*Transport, error) {
	if opts.PrivateKey == (polyamide.PrivateKey{}) {
		return nil, fmt.Errorf("local private key: %w", polyamide.ErrInvalidKey)
	}
	local, err := ecdh.X25519().NewPrivateKey(opts.PrivateKey[:])
	if err != nil {
		return nil, fmt.Errorf("local private key: %w", polyamide.ErrInvalidKey)
	}
	newBind := opts.NewBind
	if newBind == nil {
		newBind = conn.NewDefaultBind
	}
	bind := newBind()
	if bind == nil {
		return nil, errors.New("WireGuard bind factory returned nil")
	}
	if bind.BatchSize() <= 0 {
		_ = bind.Close()
		return nil, errors.New("WireGuard bind must have a positive batch size")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ControlHandlers != nil {
		handlers := make(map[string]func(*bufio.ReadWriter) error, len(opts.ControlHandlers))
		for k, v := range opts.ControlHandlers {
			handlers[k] = v
		}
		opts.ControlHandlers = handlers
	}
	t := &Transport{opts: opts, bind: bind, localPublic: device.NoisePublicKey(local.PublicKey().Bytes()), peers: make(map[device.NoisePublicKey]*peer),
		byRaw: make(map[*device.Peer]*peer), configuredEndpoints: make(map[*peer][]conn.Endpoint), done: make(chan struct{})}
	t.batchPool.New = func() any { return new(routeBatch) }
	t.host = newHostSink(bind.BatchSize())
	return t, nil
}

func (t *Transport) Start(ctx context.Context, hooks polyamide.Hooks) (err error) {
	t.ops.Lock()
	defer func() {
		t.ops.Unlock()
		if err != nil && !errors.Is(err, polyamide.ErrStarted) {
			_ = t.Close()
		}
	}()
	if t.closed.Load() {
		return polyamide.ErrClosed
	}
	if t.started.Load() {
		return polyamide.ErrStarted
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	t.runContext = ctx
	t.hooks.Store(&hooks)
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			if t.opts.Debug {
				t.opts.Logger.Debug(fmt.Sprintf(format, args...))
			}
		},
		Errorf: func(format string, args ...any) { t.opts.Logger.Error(fmt.Sprintf(format, args...)) },
	}
	dev := device.NewDeviceWithHooks(t.host, t.bind, logger, device.Hooks{Receive: t.receive, EndpointLearned: t.endpointLearned})
	t.mu.Lock()
	t.dev = dev
	t.mu.Unlock()
	if err = dev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\n", hex.EncodeToString(t.opts.PrivateKey[:]), t.opts.ListenPort)); err != nil {
		return err
	}
	for _, p := range t.peers {
		if err = t.attachPeer(dev, p); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = dev.Up(); err != nil {
		return err
	}
	for op, handler := range t.opts.ControlHandlers {
		dev.IpcHandler[op] = handler
	}
	if t.closed.Load() {
		return polyamide.ErrClosed
	}
	t.started.Store(true)
	t.stopContext = context.AfterFunc(ctx, func() { _ = t.Close() })
	return nil
}

func (t *Transport) attachPeer(dev *device.Device, p *peer) error {
	raw, err := dev.NewPeer(p.public)
	if err != nil {
		return err
	}
	raw.SetPreferRoaming(p.passive)
	raw.SetEndpoints(slices.Clone(t.configuredEndpoints[p]))
	t.mu.Lock()
	p.raw = raw
	t.byRaw[raw] = p
	t.mu.Unlock()
	if t.started.Load() {
		raw.Start()
	}
	return nil
}

func (t *Transport) PreparePeer(ctx context.Context, cfg polyamide.PeerConfig) (polyamide.Peer, error) {
	t.ops.Lock()
	defer t.ops.Unlock()
	if t.closed.Load() {
		return nil, polyamide.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.ID == "" {
		return nil, polyamide.ErrInvalidKey
	}
	public := device.NoisePublicKey(cfg.PublicKey)
	if public == (device.NoisePublicKey{}) {
		return nil, fmt.Errorf("peer key: %w", polyamide.ErrInvalidKey)
	}
	if public == t.localPublic {
		return nil, fmt.Errorf("peer is local identity: %w", polyamide.ErrInvalidKey)
	}
	if p := t.peers[public]; p != nil {
		if p.id != cfg.ID {
			return nil, fmt.Errorf("public key is bound to another ID: %w", polyamide.ErrInvalidKey)
		}
		p.passive = cfg.Passive
		if p.raw != nil {
			p.raw.SetPreferRoaming(cfg.Passive)
		}
		return p, nil
	}
	p := &peer{owner: t, id: cfg.ID, public: public, passive: cfg.Passive}
	if t.dev != nil {
		if err := t.attachPeer(t.dev, p); err != nil {
			return nil, err
		}
	}
	t.mu.Lock()
	t.peers[public] = p
	t.mu.Unlock()
	return p, nil
}

func (t *Transport) checkPeer(value polyamide.Peer) (*peer, error) {
	if t.closed.Load() {
		return nil, polyamide.ErrClosed
	}
	p, ok := value.(*peer)
	if !ok || p == nil || p.owner != t || p.retired.Load() {
		return nil, polyamide.ErrInvalidHandle
	}
	return p, nil
}

func (t *Transport) SetEndpoints(ctx context.Context, value polyamide.Peer, locators []polyamide.Endpoint) error {
	t.ops.Lock()
	defer t.ops.Unlock()
	p, err := t.checkPeer(value)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	endpoints := make([]conn.Endpoint, len(locators))
	for i, value := range locators {
		endpoint, err := t.resolveEndpoint(value)
		if err != nil {
			return err
		}
		if endpoint == nil {
			return polyamide.ErrInvalidEndpoint
		}
		endpoints[i] = endpoint
	}
	t.mu.Lock()
	t.configuredEndpoints[p] = endpoints
	t.mu.Unlock()
	if p.raw != nil {
		p.endpointMu.Lock()
		p.raw.SetEndpoints(slices.Clone(endpoints))
		p.endpointMu.Unlock()
	}
	return nil
}

func (t *Transport) RemovePeer(ctx context.Context, value polyamide.Peer) error {
	t.ops.Lock()
	defer t.ops.Unlock()
	if t.closed.Load() {
		return polyamide.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, ok := value.(*peer)
	if !ok || p == nil || p.owner != t {
		return polyamide.ErrInvalidHandle
	}
	if p.retired.Swap(true) {
		return nil
	}
	t.mu.Lock()
	delete(t.peers, p.public)
	delete(t.byRaw, p.raw)
	delete(t.configuredEndpoints, p)
	t.mu.Unlock()
	// Callbacks also lock mu, so unlock it before waiting for the peer to stop.
	if t.dev != nil {
		t.dev.RemovePeer(p.public)
	}
	return nil
}

func (t *Transport) ControlMTU(value polyamide.Peer, selected polyamide.Endpoint) (int, error) {
	_, err := t.checkPeer(value)
	if err != nil {
		return 0, err
	}
	if _, err := t.resolveEndpoint(selected); err != nil {
		return 0, err
	}
	return maxControlPayload, nil
}

// ListenPort reports the bound UDP port, or zero before Start.
func (t *Transport) ListenPort() uint16 {
	t.mu.RLock()
	dev := t.dev
	t.mu.RUnlock()
	if dev == nil || !t.started.Load() {
		return 0
	}
	return dev.ListenPort()
}

func (t *Transport) Peers() []polyamide.PeerStats {
	t.mu.RLock()
	peers := make([]*peer, 0, len(t.peers))
	for _, p := range t.peers {
		peers = append(peers, p)
	}
	t.mu.RUnlock()
	// A replaced peer shares its ID with the replacement until it is removed.
	slices.SortFunc(peers, func(a, b *peer) int {
		return cmp.Or(cmp.Compare(a.id, b.id), bytes.Compare(a.public[:], b.public[:]))
	})
	stats := make([]polyamide.PeerStats, 0, len(peers))
	for _, p := range peers {
		stat := polyamide.PeerStats{Peer: p}
		t.mu.RLock()
		raw := p.raw
		t.mu.RUnlock()
		if raw != nil {
			s := raw.Status()
			stat.LastReceived = raw.LastReceivedPacket()
			if stat.LastReceived.UnixNano() == 0 {
				stat.LastReceived = time.Time{}
			}
			stat.LastHandshake = s.LatestHandshakeTime()
			stat.TxBytes, stat.RxBytes = s.TxBytes, s.RxBytes
			if s.Endpoint != "" {
				stat.PreferredEndpoint, _ = t.PrepareEndpoint(context.Background(), s.Endpoint)
			}
			stat.Keepalive = time.Duration(s.PersistentKeepaliveInterval) * time.Second
		}
		stats = append(stats, stat)
	}
	return stats
}

func (t *Transport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		<-t.done
		return nil
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	if t.stopContext != nil {
		t.stopContext()
	}
	// Stop accepting submissions before waiting for workers. Replies attempted
	// during shutdown return ErrClosed.
	t.submitMu.Lock()
	t.submitMu.Unlock()
	t.submissions.Wait()
	if t.dev != nil {
		t.dev.Close()
	} else {
		_ = t.bind.Close()
	}
	t.mu.Lock()
	for _, p := range t.peers {
		p.retired.Store(true)
	}
	clear(t.peers)
	clear(t.byRaw)
	clear(t.configuredEndpoints)
	t.mu.Unlock()
	close(t.done)
	return nil
}

func (t *Transport) HandleUAPI(c net.Conn) {
	if !t.started.Load() || t.closed.Load() {
		_ = c.Close()
		return
	}
	t.mu.RLock()
	dev := t.dev
	t.mu.RUnlock()
	dev.IpcHandle(c)
}
