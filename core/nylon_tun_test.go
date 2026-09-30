package core

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun/tuntest"
	"github.com/encodeous/nylon/state"
	"github.com/gaissmai/bart"
	"github.com/stretchr/testify/require"
	"net/netip"
)

type testTUN struct {
	input  chan []byte
	output chan []byte
	closed chan struct{}
	once   sync.Once
}

func (h *testTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	select {
	case p := <-h.input:
		sizes[0] = copy(bufs[0][offset:], p)
		return 1, nil
	case <-h.closed:
		return 0, io.EOF
	}
}
func (h *testTUN) Write(bufs [][]byte, offset int) (int, error) {
	if offset < 10 {
		return 0, io.ErrShortBuffer
	}
	for _, p := range bufs {
		h.output <- append([]byte(nil), p[offset:]...)
	}
	return len(bufs), nil
}
func (*testTUN) MTU() (int, error)     { return 1420, nil }
func (*testTUN) Name() (string, error) { return "test-host", nil }
func (*testTUN) BatchSize() int        { return 1 }
func (h *testTUN) Close() error        { h.once.Do(func() { close(h.closed) }); return nil }

type tunTransport struct {
	polyamide.Transport
	packets chan []byte
	closed  bool
}

func (t *tunTransport) SendPackets(_ context.Context, packets []polyamide.OutboundPacket) error {
	for _, packet := range packets {
		t.packets <- append([]byte(nil), packet.Bytes...)
	}
	return nil
}
func (t *tunTransport) Close() error { t.closed = true; return nil }

func TestNylonOwnsTUNIOAndShutdown(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	host := &testTUN{input: make(chan []byte, 1), output: make(chan []byte, 1), closed: make(chan struct{})}
	transport := &tunTransport{packets: make(chan []byte, 1)}
	secondary := &tunTransport{packets: make(chan []byte, 1)}
	n := &Nylon{Context: ctx, Cancel: cancel, Tun: host, Transports: []polyamide.Transport{transport, secondary}, Log: slog.Default()}
	n.startTUNReader()
	defer func() { cancel(context.Canceled); require.NoError(t, n.cleanupTransport()) }()
	packet := tuntest.Ping(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"))
	peer := &routingPeer{transport: transport, id: "remote"}
	table := new(bart.Table[RouteTableEntry])
	table.Insert(netip.MustParsePrefix("10.0.0.2/32"), RouteTableEntry{Nh: "remote"})
	n.router.Tables.Store(&ForwardingTables{Forward: table, Exit: new(bart.Table[RouteTableEntry]), Links: map[state.NodeId][]Link{"remote": {{Peer: peer}}}})
	host.input <- packet
	select {
	case got := <-transport.packets:
		require.Equal(t, packet, got)
	case <-time.After(time.Second):
		t.Fatal("Nylon did not submit host packet")
	}
	require.NoError(t, n.deliverTUN([][]byte{packet}))
	require.Equal(t, packet, <-host.output)
	// Closing a transport cannot close the host interface.
	require.NoError(t, transport.Close())
	select {
	case <-host.closed:
		t.Fatal("transport closed the host")
	default:
	}
	cancel(context.Canceled)
	require.NoError(t, n.cleanupTransport())
	require.True(t, transport.closed)
	require.True(t, secondary.closed)
	select {
	case <-host.closed:
	default:
		t.Fatal("Nylon did not close the host")
	}
}

type routingPeer struct {
	transport polyamide.Transport
	id        string
}

func (p *routingPeer) Transport() polyamide.Transport { return p.transport }
func (p *routingPeer) ID() string                     { return p.id }
func (*routingPeer) PublicKey() polyamide.PublicKey   { return polyamide.PublicKey{} }

func TestTUNRoutesEachDestinationToItsTransport(t *testing.T) {
	a := &tunTransport{packets: make(chan []byte, 4)}
	b := &tunTransport{packets: make(chan []byte, 4)}
	host := &testTUN{output: make(chan []byte, 4)}
	n := &Nylon{Context: context.Background(), Transports: []polyamide.Transport{a, b}, Tun: host}
	forward := new(bart.Table[RouteTableEntry])
	exit := new(bart.Table[RouteTableEntry])
	first := &routingPeer{transport: a, id: "a"}
	second := &routingPeer{transport: b, id: "b"}
	forward.Insert(netip.MustParsePrefix("10.0.0.1/32"), RouteTableEntry{Nh: "a"})
	forward.Insert(netip.MustParsePrefix("10.0.0.2/32"), RouteTableEntry{Nh: "b"})
	forward.Insert(netip.MustParsePrefix("10.0.0.3/32"), RouteTableEntry{Blackhole: true})
	exit.Insert(netip.MustParsePrefix("10.0.0.4/32"), RouteTableEntry{})
	n.router.Tables.Store(&ForwardingTables{Forward: forward, Exit: exit, Links: map[state.NodeId][]Link{"a": {{Peer: first}}, "b": {{Peer: second}}}})
	packet := func(dest string) []byte {
		return tuntest.Ping(netip.MustParseAddr(dest), netip.MustParseAddr("10.1.0.1"))
	}
	one, two, drop, local, unknown := packet("10.0.0.1"), packet("10.0.0.2"), packet("10.0.0.3"), packet("10.0.0.4"), packet("10.0.0.5")
	require.NoError(t, n.routeTUNPackets([][]byte{one, two, drop, local, unknown}))
	require.Equal(t, one, <-a.packets)
	require.Equal(t, two, <-b.packets)
	require.Equal(t, local, <-host.output)
	require.Empty(t, a.packets)
	require.Empty(t, b.packets)
	// Changing the published forwarding table changes the backend immediately.
	next := new(bart.Table[RouteTableEntry])
	next.Insert(netip.MustParsePrefix("10.0.0.1/32"), RouteTableEntry{Nh: "b"})
	n.router.Tables.Store(&ForwardingTables{Forward: next, Exit: exit, Links: map[state.NodeId][]Link{"b": {{Peer: second}}}})
	require.NoError(t, n.routeTUNPackets([][]byte{one}))
	require.Equal(t, one, <-b.packets)
	require.Empty(t, a.packets)
}

// byteCountTUN reports bytes written, like the Linux TUN.
type byteCountTUN struct {
	*testTUN
	minCap int
}

func (h *byteCountTUN) Write(bufs [][]byte, offset int) (int, error) {
	total := 0
	h.minCap = -1
	for _, b := range bufs {
		total += len(b) - offset
		if h.minCap < 0 || cap(b) < h.minCap {
			h.minCap = cap(b)
		}
	}
	_, err := h.testTUN.Write(bufs, offset)
	return total, err
}

func TestDeliverTUNAcceptsByteCounts(t *testing.T) {
	host := &byteCountTUN{testTUN: &testTUN{output: make(chan []byte, 2)}}
	n := &Nylon{Tun: host}
	packet := tuntest.Ping(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"))
	require.NoError(t, n.deliverTUN([][]byte{packet, packet}))
	require.Equal(t, packet, <-host.output)
	// Spare capacity lets Linux GRO coalesce the packets that follow.
	require.GreaterOrEqual(t, host.minCap, tunHeadroom+maxTUNPacketSize)
}
