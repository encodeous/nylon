package adapter_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/adapter"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun/tuntest"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func privateKey(t *testing.T) [32]byte {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	require.NoError(t, err)
	return [32]byte(k.Bytes())
}

func publicKey(t *testing.T, key [32]byte) polyamide.PublicKey {
	t.Helper()
	k, err := ecdh.X25519().NewPrivateKey(key[:])
	require.NoError(t, err)
	return polyamide.PublicKey(k.PublicKey().Bytes())
}

func newTransport(t *testing.T, key [32]byte) *adapter.Transport {
	t.Helper()
	wg, err := adapter.New(adapter.Options{PrivateKey: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, wg.Close()) })
	return wg
}

type pair struct {
	transports [2]*adapter.Transport
	peers      [2]polyamide.Peer
	paths      [2]polyamide.Endpoint
}

func newPair(t *testing.T, hooks [2]polyamide.Hooks) pair {
	t.Helper()
	ctx := context.Background()
	keys := [2][32]byte{privateKey(t), privateKey(t)}
	var p pair
	for i := range p.transports {
		p.transports[i] = newTransport(t, keys[i])
		peer, err := p.transports[i].PreparePeer(ctx, polyamide.PeerConfig{ID: []string{"b", "a"}[i], PublicKey: publicKey(t, keys[i^1])})
		require.NoError(t, err)
		p.peers[i] = peer
	}
	for i := range p.transports {
		require.NoError(t, p.transports[i].Start(ctx, hooks[i]))
	}
	for i := range p.transports {
		port := p.transports[i^1].ListenPort()
		endpoint, err := p.transports[i].PrepareEndpoint(ctx, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port).String())
		require.NoError(t, err)
		p.paths[i] = endpoint
		require.NoError(t, p.transports[i].SetEndpoints(ctx, p.peers[i], []polyamide.Endpoint{endpoint}))
	}
	return p
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for transport delivery")
		var zero T
		return zero
	}
}

func TestEncryptedControlAndHostPackets(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	type message struct {
		payload []byte
		peer    polyamide.Peer
		path    polyamide.Endpoint
	}
	control := make(chan message, 4)
	host := make(chan []byte, 4)
	replyErrors := make(chan error, 1)
	var p pair
	var hooks [2]polyamide.Hooks
	hooks[0].Control = func(m polyamide.ControlMessage) { control <- message{bytes.Clone(m.Payload), m.From, m.Endpoint} }
	hooks[1].Control = func(m polyamide.ControlMessage) {
		control <- message{bytes.Clone(m.Payload), m.From, m.Endpoint}
		replyErrors <- p.transports[1].SendControl(ctx, m.From, m.Endpoint, []byte("reply"))
	}
	for i := range hooks {
		hooks[i].RouteBatch = func(packets []polyamide.TCElement, decisions []polyamide.TCDecision) {
			for j, packet := range packets {
				if packet.Incoming() {
					packet.DecrementTTL()
					decisions[j].Action = polyamide.TcBounce
				} else {
					decisions[j] = polyamide.TCDecision{Action: polyamide.TcForward, To: p.peers[i]}
				}
			}
		}
		hooks[i].DeliverHost = func(packets [][]byte) error {
			for _, packet := range packets {
				host <- bytes.Clone(packet)
			}
			return nil
		}
	}
	p = newPair(t, hooks)
	payload := []byte("request")
	require.NoError(t, p.transports[0].SendControl(ctx, p.peers[0], p.paths[0], payload))
	copy(payload, []byte("changed")) // caller storage is reusable immediately
	request := receive(t, control)
	require.Equal(t, "request", string(request.payload))
	require.Same(t, p.peers[1], request.peer)
	require.Equal(t, p.paths[1], request.path)
	require.NoError(t, receive(t, replyErrors))
	reply := receive(t, control)
	require.Equal(t, "reply", string(reply.payload))
	require.Same(t, p.peers[0], reply.peer)

	packet := tuntest.Ping(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"))
	expected := bytes.Clone(packet)
	polyamide.TCElement{Bytes: expected}.DecrementTTL()
	require.NoError(t, p.transports[0].SendPackets(ctx, []polyamide.OutboundPacket{{Bytes: packet, To: p.peers[0]}}))
	clear(packet)
	require.Equal(t, expected, receive(t, host))
	stat := p.transports[1].Peers()[0]
	require.Greater(t, stat.RxBytes, uint64(0))
	require.False(t, stat.LastHandshake.IsZero())
	require.False(t, stat.LastReceived.IsZero())
}

func TestPeerReplacementAndHandleValidation(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	wg := newTransport(t, privateKey(t))
	cfg := polyamide.PeerConfig{ID: "remote", PublicKey: publicKey(t, privateKey(t))}
	old, err := wg.PreparePeer(ctx, cfg)
	require.NoError(t, err)
	oldPath, err := wg.PrepareEndpoint(ctx, "127.0.0.1:12345")
	require.NoError(t, err)
	require.NoError(t, wg.SetEndpoints(ctx, old, []polyamide.Endpoint{oldPath}))
	clear(cfg.PublicKey[:])
	require.NotEqual(t, cfg.PublicKey, old.PublicKey())
	copyOfKey := old.PublicKey()
	clear(copyOfKey[:])
	require.NotEqual(t, copyOfKey, old.PublicKey())
	unchanged, err := wg.PreparePeer(ctx, polyamide.PeerConfig{ID: "remote", PublicKey: old.PublicKey()})
	require.NoError(t, err)
	require.Same(t, old, unchanged)
	require.NoError(t, wg.Start(ctx, polyamide.Hooks{}))

	replacement, err := wg.PreparePeer(ctx, polyamide.PeerConfig{ID: "remote", PublicKey: publicKey(t, privateKey(t))})
	require.NoError(t, err)
	require.Equal(t, old.ID(), replacement.ID())
	require.NotSame(t, old, replacement)
	require.Len(t, wg.Peers(), 2)
	require.NoError(t, wg.SetEndpoints(ctx, replacement, []polyamide.Endpoint{oldPath}))
	other := newTransport(t, privateKey(t))
	require.ErrorIs(t, other.SetEndpoints(ctx, old, nil), polyamide.ErrInvalidHandle)
	require.NoError(t, wg.RemovePeer(ctx, old))
	require.NoError(t, wg.RemovePeer(ctx, old))
	require.Len(t, wg.Peers(), 1)
	require.ErrorIs(t, wg.SendControl(ctx, old, oldPath, nil), polyamide.ErrInvalidHandle)
	err = wg.SetEndpoints(ctx, old, []polyamide.Endpoint{oldPath})
	require.ErrorIs(t, err, polyamide.ErrInvalidHandle)
	recreated, err := wg.PreparePeer(ctx, polyamide.PeerConfig{ID: "remote", PublicKey: old.PublicKey()})
	require.NoError(t, err)
	require.NotSame(t, old, recreated)
}

func TestControlLimitsAndUnsupportedInputs(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	wg := newTransport(t, privateKey(t))
	for _, cfg := range []polyamide.PeerConfig{
		{ID: "bad", PublicKey: polyamide.PublicKey{}},
		{PublicKey: publicKey(t, privateKey(t))},
	} {
		_, err := wg.PreparePeer(ctx, cfg)
		require.ErrorIs(t, err, polyamide.ErrInvalidKey)
	}

	p, err := wg.PreparePeer(ctx, polyamide.PeerConfig{ID: "peer", PublicKey: publicKey(t, privateKey(t))})
	require.NoError(t, err)
	require.ErrorIs(t, wg.SendControl(ctx, p, nil, nil), polyamide.ErrNotStarted)
	_, err = wg.PrepareEndpoint(ctx, "example.invalid:12345")
	require.ErrorIs(t, err, polyamide.ErrInvalidEndpoint)
	path, err := wg.PrepareEndpoint(ctx, "[::1]:12345")
	require.NoError(t, err)
	require.NoError(t, wg.Start(ctx, polyamide.Hooks{}))
	require.ErrorIs(t, wg.Start(ctx, polyamide.Hooks{}), polyamide.ErrStarted)
	require.ErrorIs(t, wg.SendControl(ctx, p, nil, nil), polyamide.ErrNoEndpoint)
	mtu, err := wg.ControlMTU(p, path)
	require.NoError(t, err)
	require.ErrorIs(t, wg.SendControl(ctx, p, path, make([]byte, mtu+1)), polyamide.ErrMessageTooLarge)
	require.NoError(t, wg.SetEndpoints(ctx, p, []polyamide.Endpoint{path}))
	require.NoError(t, wg.SendControl(ctx, p, path, make([]byte, mtu)))
	require.NoError(t, wg.Close())
	require.NoError(t, wg.Close())
	require.ErrorIs(t, wg.SendPackets(ctx, nil), polyamide.ErrClosed)
}

func TestCloseJoinsCallbackWithImmediateReply(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	entered, release := make(chan struct{}), make(chan struct{})
	errors := make(chan error, 1)
	var p pair
	var once sync.Once
	hooks := [2]polyamide.Hooks{{}, {Control: func(m polyamide.ControlMessage) {
		once.Do(func() { close(entered) })
		<-release
		errors <- p.transports[1].SendControl(context.Background(), m.From, m.Endpoint, []byte("reply"))
	}}}
	p = newPair(t, hooks)
	require.NoError(t, p.transports[0].SendControl(context.Background(), p.peers[0], p.paths[0], []byte("request")))
	receive(t, entered)
	closed := make(chan struct{})
	go func() { _ = p.transports[1].Close(); close(closed) }()
	require.Eventually(t, func() bool { return errorsIsClosed(p.transports[1].SendPackets(context.Background(), nil)) }, time.Second, time.Millisecond)
	select {
	case <-closed:
		t.Fatal("Close returned before its callback finished")
	default:
	}
	close(release)
	require.ErrorIs(t, receive(t, errors), polyamide.ErrClosed)
	receive(t, closed)
}

func errorsIsClosed(err error) bool { return errors.Is(err, polyamide.ErrClosed) }

type failingBind struct {
	conn.Bind
	failure error
}

func (b failingBind) Open(uint16) ([]conn.ReceiveFunc, uint16, error) { return nil, 0, b.failure }

func TestStartFailureAndContextLifetime(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	failure := errors.New("bind failure")
	wg, err := adapter.New(adapter.Options{PrivateKey: privateKey(t), NewBind: func() conn.Bind { return failingBind{Bind: conn.NewDefaultBind(), failure: failure} }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	require.ErrorIs(t, wg.Start(context.Background(), polyamide.Hooks{}), failure)
	require.NoError(t, wg.Close())
	require.ErrorIs(t, wg.SendPackets(context.Background(), nil), polyamide.ErrClosed)

	wg = newTransport(t, privateKey(t))
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, wg.Start(ctx, polyamide.Hooks{}))
	cancel()
	require.Eventually(t, func() bool { return errors.Is(wg.SendPackets(context.Background(), nil), polyamide.ErrClosed) }, time.Second, time.Millisecond)
	require.NoError(t, wg.Close())
}

type trackedBind struct {
	conn.Bind
	opened    int
	closed    int
	batchSize int
}

func (b *trackedBind) BatchSize() int { return b.batchSize }
func (b *trackedBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.opened++
	return b.Bind.Open(port)
}
func (b *trackedBind) Close() error { b.closed++; return b.Bind.Close() }

func TestTransportCreatesAndOwnsBind(t *testing.T) {
	bind := &trackedBind{Bind: conn.NewDefaultBind(), batchSize: 1}
	created := 0
	wg, err := adapter.New(adapter.Options{PrivateKey: privateKey(t), NewBind: func() conn.Bind { created++; return bind }})
	require.NoError(t, err)
	require.Equal(t, 1, created)
	require.Zero(t, bind.opened, "construction must not listen")
	require.NoError(t, wg.Close())
	require.NoError(t, wg.Close())
	require.Equal(t, 1, bind.closed, "unstarted bind is owned and closed once")
	invalid := &trackedBind{Bind: conn.NewDefaultBind()}
	_, err = adapter.New(adapter.Options{PrivateKey: privateKey(t), NewBind: func() conn.Bind { return invalid }})
	require.Error(t, err)
	require.Equal(t, 1, invalid.closed, "failed construction must release its bind")
	_, err = adapter.New(adapter.Options{PrivateKey: privateKey(t), NewBind: func() conn.Bind { return nil }})
	require.Error(t, err)
}

func TestExplicitEndpointStartsPeerAndReplyUsesReceivedValue(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	received := make(chan []byte, 2)
	replies := make(chan error, 1)
	var p pair
	hooks := [2]polyamide.Hooks{
		{Control: func(m polyamide.ControlMessage) { received <- bytes.Clone(m.Payload) }},
		{Control: func(m polyamide.ControlMessage) {
			replies <- p.transports[1].SendControl(ctx, m.From, m.Endpoint, []byte("reply"))
		}},
	}
	p = newPair(t, hooks)
	for i := range p.transports {
		require.NoError(t, p.transports[i].SetEndpoints(ctx, p.peers[i], nil))
	}
	require.NoError(t, p.transports[0].SendControl(ctx, p.peers[0], p.paths[0], []byte("request")))
	require.NoError(t, receive(t, replies))
	require.Equal(t, []byte("reply"), receive(t, received))
	// Endpoints can be reused by peer generations on the same transport.
	endpoint := p.paths[0]
	require.NoError(t, p.transports[0].SetEndpoints(ctx, p.peers[0], []polyamide.Endpoint{endpoint}))
	require.ErrorIs(t, p.transports[0].SetEndpoints(ctx, p.peers[0], []polyamide.Endpoint{nil}), polyamide.ErrInvalidEndpoint)
}

func TestSendPacketsSkipsInvalidPackets(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	host := make(chan []byte, 4)
	var hooks [2]polyamide.Hooks
	hooks[1].RouteBatch = func(packets []polyamide.TCElement, decisions []polyamide.TCDecision) {
		for i := range packets {
			decisions[i].Action = polyamide.TcBounce
		}
	}
	hooks[1].DeliverHost = func(packets [][]byte) error {
		for _, packet := range packets {
			host <- bytes.Clone(packet)
		}
		return nil
	}
	p := newPair(t, hooks)
	// A peer without an endpoint cannot be sent to, but it must not drop the rest of the batch.
	unreachable, err := p.transports[0].PreparePeer(ctx, polyamide.PeerConfig{ID: "unreachable", PublicKey: publicKey(t, privateKey(t))})
	require.NoError(t, err)
	packet := tuntest.Ping(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"))
	err = p.transports[0].SendPackets(ctx, []polyamide.OutboundPacket{{Bytes: packet, To: unreachable}, {Bytes: packet, To: p.peers[0]}})
	require.ErrorIs(t, err, polyamide.ErrNoEndpoint)
	require.Equal(t, packet, receive(t, host))
}

func TestEndpointLearnedReportsUnconfiguredSourceOnce(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	learned := make(chan polyamide.Endpoint, 8)
	control := make(chan struct{}, 8)
	var hooks [2]polyamide.Hooks
	hooks[1].EndpointLearned = func(_ polyamide.Peer, endpoint polyamide.Endpoint) { learned <- endpoint }
	hooks[1].Control = func(polyamide.ControlMessage) { control <- struct{}{} }
	p := newPair(t, hooks)
	// Transport 1 only knows a stale address for transport 0.
	stale, err := p.transports[1].PrepareEndpoint(ctx, "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, p.transports[1].SetEndpoints(ctx, p.peers[1], []polyamide.Endpoint{stale}))
	for range 3 {
		require.NoError(t, p.transports[0].SendControl(ctx, p.peers[0], p.paths[0], []byte("hello")))
		receive(t, control)
	}
	endpoint := receive(t, learned)
	require.Equal(t, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), p.transports[0].ListenPort()).String(), endpoint.Address())
	require.Empty(t, learned)
}

func TestUAPIIsReadOnly(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	wg := newTransport(t, privateKey(t))
	remote := publicKey(t, privateKey(t))
	peer, err := wg.PreparePeer(ctx, polyamide.PeerConfig{ID: "remote", PublicKey: remote})
	require.NoError(t, err)
	require.NoError(t, wg.Start(ctx, polyamide.Hooks{}))
	client, server := net.Pipe()
	defer client.Close()
	go wg.HandleUAPI(server)
	reader := bufio.NewReader(client)
	response := func() string {
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			require.NoError(t, err)
			if line == "\n" {
				return strings.Join(lines, "")
			}
			lines = append(lines, line)
		}
	}
	// Removing a peer through UAPI would leave the transport's handle stale.
	_, err = fmt.Fprintf(client, "set=1\npublic_key=%s\nremove=true\n\n", hex.EncodeToString(remote[:]))
	require.NoError(t, err)
	require.NotContains(t, response(), "errno=0")
	_, err = io.WriteString(client, "get=1\n\n")
	require.NoError(t, err)
	status := response()
	require.Contains(t, status, "public_key="+hex.EncodeToString(remote[:]))
	require.Contains(t, status, "errno=0")
	require.Len(t, wg.Peers(), 1)
	require.Same(t, peer, wg.Peers()[0].Peer)
}
