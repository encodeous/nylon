package core

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/adapter"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun/tuntest"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/gaissmai/bart"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
)

func routingBackend(t *testing.T) (*adapter.Transport, polyamide.PublicKey) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	require.NoError(t, err)
	backend, err := adapter.New(adapter.Options{PrivateKey: polyamide.PrivateKey(key.Bytes()), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, backend.Close()) })
	return backend, polyamide.PublicKey(key.PublicKey().Bytes())
}
func routingReceive(t *testing.T, packets <-chan []byte) []byte {
	t.Helper()
	select {
	case p := <-packets:
		return p
	case <-time.After(5 * time.Second):
		t.Fatal("timed out receiving routed packet")
		return nil
	}
}

func TestNylonForwardsAcrossTransports(t *testing.T) {
	t.Cleanup(func() { goleak.VerifyNone(t) })
	ctx := context.Background()
	var local, remote [2]*adapter.Transport
	var localKey, remoteKey [2]polyamide.PublicKey
	var localPeer, remotePeer [2]polyamide.Peer
	var received [2]chan []byte
	var control [2]chan []byte
	var endpoints [2]polyamide.Endpoint
	n := &Nylon{Context: ctx}
	forward := new(bart.Table[RouteTableEntry])
	for i := range local {
		local[i], localKey[i] = routingBackend(t)
		remote[i], remoteKey[i] = routingBackend(t)
		var err error
		// Equal IDs on distinct transports must remain distinct handles.
		localPeer[i], err = local[i].PreparePeer(ctx, polyamide.PeerConfig{ID: "remote", PublicKey: remoteKey[i]})
		require.NoError(t, err)
		remotePeer[i], err = remote[i].PreparePeer(ctx, polyamide.PeerConfig{ID: "nylon", PublicKey: localKey[i]})
		require.NoError(t, err)
		destination := netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)})
		forward.Insert(netip.PrefixFrom(destination, 32), RouteTableEntry{Nh: state.NodeId([]string{"a", "b"}[i])})
		received[i] = make(chan []byte, 4)
		control[i] = make(chan []byte, 4)
	}
	n.Transports = []polyamide.Transport{local[0], local[1]}
	n.router.Tables.Store(&ForwardingTables{Forward: forward, Exit: new(bart.Table[RouteTableEntry]), Links: map[state.NodeId][]Link{"a": {{Peer: localPeer[0]}}, "b": {{Peer: localPeer[1]}}}})
	require.NotEqual(t, localPeer[0], localPeer[1])
	require.Equal(t, localPeer[0].ID(), localPeer[1].ID())
	for i := range local {
		require.NoError(t, local[i].Start(ctx, n.transportHooks()))
		require.NoError(t, remote[i].Start(ctx, polyamide.Hooks{
			Control: func(m polyamide.ControlMessage) { control[i] <- bytes.Clone(m.Payload) },
			RouteBatch: func(p []polyamide.TCElement, d []polyamide.TCDecision) {
				for j := range p {
					d[j].Action = polyamide.TcBounce
				}
			},
			DeliverHost: func(p [][]byte) error {
				for _, packet := range p {
					received[i] <- bytes.Clone(packet)
				}
				return nil
			},
		}))
	}
	for i := range local {
		for _, pair := range []struct {
			from, to *adapter.Transport
			peer     polyamide.Peer
		}{{local[i], remote[i], localPeer[i]}, {remote[i], local[i], remotePeer[i]}} {
			endpoint, err := pair.from.PrepareEndpoint(ctx, netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), pair.to.ListenPort()).String())
			require.NoError(t, err)
			require.NoError(t, pair.from.SetEndpoints(ctx, pair.peer, []polyamide.Endpoint{endpoint}))
			if pair.from == local[i] {
				endpoints[i] = endpoint
			}
		}
	}
	first := tuntest.Ping(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.1.0.1"))
	second := tuntest.Ping(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.1.0.1"))
	require.NoError(t, n.routeTUNPackets([][]byte{first, second}))
	require.Equal(t, first, routingReceive(t, received[0]))
	require.Equal(t, second, routingReceive(t, received[1]))
	// Receive on A and forward through B. Decrement TTL once.
	require.NoError(t, remote[0].SendPackets(ctx, []polyamide.OutboundPacket{{Bytes: second, To: remotePeer[0]}}))
	expected := bytes.Clone(second)
	polyamide.TCElement{Bytes: expected}.DecrementTTL()
	require.Equal(t, expected, routingReceive(t, received[1]))
	// Forwarding within A retains the same engine buffer and the same TTL policy.
	require.NoError(t, remote[0].SendPackets(ctx, []polyamide.OutboundPacket{{Bytes: first, To: remotePeer[0]}}))
	expected = bytes.Clone(first)
	polyamide.TCElement{Bytes: expected}.DecrementTTL()
	require.Equal(t, expected, routingReceive(t, received[0]))
	// Control messages use the peer's owning transport.
	bundle := &protocol.TransportBundle{Packets: []*protocol.Ny{{}}}
	require.NoError(t, n.SendNylonBundle(bundle, nil, localPeer[1]))
	data, err := proto.Marshal(bundle)
	require.NoError(t, err)
	require.Equal(t, data, routingReceive(t, control[1]))
	require.Empty(t, control[0])
	// One logical neighbour can choose between both transport instances.
	tunablesA := state.DefaultRouterTunables()
	tunablesA.MinimumConfidenceWindow = 1
	tunablesB := tunablesA
	tunablesB.LinkDeadThreshold = 200 * time.Millisecond
	healthA := state.NewEndpoint(endpoints[0].Address(), false, &tunablesA)
	healthB := state.NewEndpoint(endpoints[1].Address(), false, &tunablesB)
	healthA.Renew()
	healthA.UpdatePing(10 * time.Millisecond)
	healthB.Renew()
	healthB.UpdatePing(time.Millisecond)
	n.neighbourLinks = map[state.NodeId][]neighbourLink{"remote": {
		{Link: Link{Peer: localPeer[0], Endpoint: endpoints[0]}, health: healthA},
		{Link: Link{Peer: localPeer[1], Endpoint: endpoints[1]}, health: healthB},
	}}
	forward = new(bart.Table[RouteTableEntry])
	forward.Insert(netip.MustParsePrefix("10.0.0.1/32"), RouteTableEntry{Nh: "remote"})
	n.router.Tables.Store(&ForwardingTables{Forward: forward, Exit: new(bart.Table[RouteTableEntry])})
	n.publishLinks()
	snapshot := n.router.Tables.Load()
	require.Same(t, localPeer[1], snapshot.Links["remote"][0].Peer)
	require.NoError(t, n.routeTUNPackets([][]byte{first}))
	require.Equal(t, first, routingReceive(t, received[1]))
	// Expiring B changes only link selection. The logical next hop stays the same.
	<-time.After(210 * time.Millisecond)
	n.publishLinks()
	require.Same(t, forward, n.router.Tables.Load().Forward)
	require.Same(t, localPeer[1], snapshot.Links["remote"][0].Peer)
	require.NoError(t, n.routeTUNPackets([][]byte{first}))
	require.Equal(t, first, routingReceive(t, received[0]))
	require.ErrorIs(t, local[0].SendPackets(ctx, []polyamide.OutboundPacket{{Bytes: first, To: localPeer[0], Endpoint: endpoints[1]}}), polyamide.ErrInvalidEndpoint)
	// Foreign destinations are rejected before transport submission.
	require.ErrorIs(t, local[0].SendPackets(ctx, []polyamide.OutboundPacket{{Bytes: first, To: localPeer[1]}}), polyamide.ErrInvalidHandle)
}

func TestRuntimeLinksAllowMultipleTransportsAndRetireRemovedBinding(t *testing.T) {
	a, _ := routingBackend(t)
	b, _ := routingBackend(t)
	_, remoteKey := routingBackend(t)
	n := testNylonWithPrefixes()
	n.Context = context.Background()
	n.Transports = []polyamide.Transport{a, b}
	n.CentralCfg.Clients = []state.ClientCfg{{NodeCfg: state.NodeCfg{Id: "remote", PubKey: state.NyPublicKey(remoteKey)}}}
	n.CentralCfg.Graph = []string{"node, remote"}
	configs := []LinkConfig{{Transport: a}, {Transport: b}}
	n.linksForNeighbour = func(state.NodeId) []LinkConfig { return configs }
	forward := new(bart.Table[RouteTableEntry])
	forward.Insert(netip.MustParsePrefix("10.0.0.1/32"), RouteTableEntry{Nh: "remote"})
	n.router.Tables.Store(&ForwardingTables{Forward: forward, Exit: new(bart.Table[RouteTableEntry])})
	require.NoError(t, n.SyncTransport())
	old := n.transportPeer("remote", a)
	other := n.transportPeer("remote", b)
	require.NotNil(t, old)
	require.NotNil(t, other)
	require.Len(t, n.router.Tables.Load().Links["remote"], 2)
	snapshot := n.router.Tables.Load()
	configs = []LinkConfig{{Transport: b}}
	require.NoError(t, n.SyncTransport())
	require.Same(t, other, n.transportPeer("remote", b))
	require.Nil(t, n.transportPeer("remote", a))
	require.Empty(t, a.Peers())
	require.Len(t, b.Peers(), 1)
	require.Len(t, snapshot.Links["remote"], 2)
	require.Same(t, forward, n.router.Tables.Load().Forward)
	require.Same(t, other, n.routePacket(polyamide.TCElement{Bytes: tuntest.Ping(netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.1.0.1"))}).To)
	configs = []LinkConfig{{Transport: nil}}
	require.ErrorContains(t, n.SyncTransport(), "unregistered transport")
	require.Same(t, other, n.transportPeer("remote", b))
}

func TestRuntimeLinksCacheEndpointsAndKeepInstanceHealthSeparate(t *testing.T) {
	a, _ := routingBackend(t)
	b, _ := routingBackend(t)
	_, remoteKey := routingBackend(t)
	n := testNylonWithPrefixes()
	n.Context = context.Background()
	n.Transports = []polyamide.Transport{a, b}
	n.EndpointResolver = state.NewEndpointResolver(nil)
	n.CentralCfg.Routers = append(n.CentralCfg.Routers, state.RouterCfg{NodeCfg: state.NodeCfg{Id: "remote", PubKey: state.NyPublicKey(remoteKey)}})
	n.CentralCfg.Graph = []string{"node, remote"}
	n.RouterState.Neighbours = []*state.Neighbour{{Id: "remote"}}
	n.linksForNeighbour = func(state.NodeId) []LinkConfig {
		return []LinkConfig{{Transport: a, Address: "127.0.0.1:1234"}, {Transport: b, Address: "127.0.0.1:1234"}}
	}
	n.router.Tables.Store(&ForwardingTables{Forward: new(bart.Table[RouteTableEntry]), Exit: new(bart.Table[RouteTableEntry])})
	require.NoError(t, n.SyncTransport())
	first := n.neighbourLinks["remote"][0]
	second := n.neighbourLinks["remote"][1]
	require.NotSame(t, first.health, second.health)
	require.NoError(t, n.SyncTransport())
	require.Same(t, first.Endpoint, n.neighbourLinks["remote"][0].Endpoint)
	require.Same(t, second.Endpoint, n.neighbourLinks["remote"][1].Endpoint)
	require.Same(t, first.health, n.receivedLink("remote", first.Endpoint, first.Peer))
	require.Same(t, second.health, n.receivedLink("remote", second.Endpoint, second.Peer))
	first.health.Renew()
	n.publishLinks()
	require.Len(t, n.router.Tables.Load().Links["remote"], 1)
	require.Same(t, first.Peer, n.router.Tables.Load().Links["remote"][0].Peer)
}

func TestDefaultLinksBindConfiguredEndpointsAndKeepLearnedOnes(t *testing.T) {
	a, _ := routingBackend(t)
	_, remoteKey := routingBackend(t)
	n := testNylonWithPrefixes()
	n.Context = context.Background()
	n.Transports = []polyamide.Transport{a}
	n.EndpointResolver = state.NewEndpointResolver(nil)
	n.CentralCfg.Routers = append(n.CentralCfg.Routers, state.RouterCfg{NodeCfg: state.NodeCfg{Id: "remote", PubKey: state.NyPublicKey(remoteKey)}, Endpoints: []string{"127.0.0.1:1234"}})
	n.CentralCfg.Graph = []string{"node, remote"}
	// Router-state reconciliation creates health for configured endpoints.
	configured := state.NewEndpoint("127.0.0.1:1234", false, &n.RouterTunables)
	n.RouterState.Neighbours = []*state.Neighbour{{Id: "remote", Eps: []state.Endpoint{configured}}}
	n.router.Tables.Store(&ForwardingTables{Forward: new(bart.Table[RouteTableEntry]), Exit: new(bart.Table[RouteTableEntry])})
	require.NoError(t, n.SyncTransport())
	require.Len(t, n.neighbourLinks["remote"], 1)
	link := n.neighbourLinks["remote"][0]
	require.Same(t, configured, link.health)
	require.Same(t, a, link.Peer.Transport())
	require.Equal(t, "127.0.0.1:1234", link.Endpoint.Address())
	// A ping from an unknown address is learned and survives the next sync.
	from, err := a.PrepareEndpoint(n.Context, "127.0.0.1:5678")
	require.NoError(t, err)
	learned := n.receivedLink("remote", from, link.Peer)
	require.NotNil(t, learned)
	require.True(t, learned.IsRemote())
	learned.Renew()
	require.NoError(t, n.SyncTransport())
	require.Len(t, n.neighbourLinks["remote"], 2)
	require.Same(t, configured, n.neighbourLinks["remote"][0].health)
	require.Same(t, learned, n.neighbourLinks["remote"][1].health)
	require.Len(t, n.RouterState.GetNeighbour("remote").Eps, 2)
	// Collecting the learned health also removes its link.
	n.RouterState.GetNeighbour("remote").Eps = []state.Endpoint{configured}
	n.pruneLinks()
	require.Len(t, n.neighbourLinks["remote"], 1)
	require.Same(t, configured, n.neighbourLinks["remote"][0].health)
}

func TestRouteDropsUnroutablePackets(t *testing.T) {
	n := testNylonWithPrefixes()
	n.router.Tables.Store(&ForwardingTables{Forward: new(bart.Table[RouteTableEntry]), Exit: new(bart.Table[RouteTableEntry])})
	incoming := polyamide.TCElement{Bytes: tuntest.Ping(netip.MustParseAddr("10.0.0.9"), netip.MustParseAddr("10.1.0.1")), From: new(testTransportPeer)}
	require.Equal(t, polyamide.TcDrop, n.routePacket(incoming).Action)
	n.UseSystemRouting = true
	require.Equal(t, polyamide.TcBounce, n.routePacket(incoming).Action)
}

func TestRouteLeavesEndpointToTransport(t *testing.T) {
	n := testNylonWithPrefixes()
	peer := new(testTransportPeer)
	forward := new(bart.Table[RouteTableEntry])
	forward.Insert(netip.MustParsePrefix("10.0.0.0/24"), RouteTableEntry{Nh: "next"})
	endpoint := testEndpoint("127.0.0.1:1234")
	n.router.Tables.Store(&ForwardingTables{Forward: forward, Exit: new(bart.Table[RouteTableEntry]), Links: map[state.NodeId][]Link{"next": {{Peer: peer, Endpoint: endpoint}}}})
	decision := n.routePacket(polyamide.TCElement{Bytes: tuntest.Ping(netip.MustParseAddr("10.0.0.9"), netip.MustParseAddr("10.1.0.1"))})
	require.Equal(t, polyamide.TcForward, decision.Action)
	require.Same(t, peer, decision.To)
	require.Nil(t, decision.Endpoint)
}

type testEndpoint string

func (e testEndpoint) Address() string { return string(e) }

func TestRuntimeValidation(t *testing.T) {
	a := &tunTransport{}
	b := &tunTransport{}
	host := &testTUN{closed: make(chan struct{})}
	require.Error(t, Runtime{Host: host}.validate())
	require.Error(t, Runtime{Transports: []polyamide.Transport{a}}.validate())
	require.Error(t, Runtime{Transports: []polyamide.Transport{a, nil}, Host: host}.validate())
	require.ErrorContains(t, Runtime{Transports: []polyamide.Transport{a, a}, Host: host}.validate(), "duplicate")
	require.NoError(t, Runtime{Transports: []polyamide.Transport{a, b}, Host: host}.validate())
}

func TestSyncTransportRebindsMovedKeyAndIsolatesFailures(t *testing.T) {
	a, localKey := routingBackend(t)
	_, keyB := routingBackend(t)
	_, keyD := routingBackend(t)
	n := testNylonWithPrefixes()
	n.Context = context.Background()
	n.Transports = []polyamide.Transport{a}
	client := func(id state.NodeId, key polyamide.PublicKey) state.ClientCfg {
		return state.ClientCfg{NodeCfg: state.NodeCfg{Id: id, PubKey: state.NyPublicKey(key)}}
	}
	n.CentralCfg.Clients = []state.ClientCfg{client("b", keyB), client("d", keyD)}
	n.CentralCfg.Graph = []string{"node, b", "node, d"}
	n.router.Tables.Store(&ForwardingTables{Forward: new(bart.Table[RouteTableEntry]), Exit: new(bart.Table[RouteTableEntry])})
	require.NoError(t, n.SyncTransport())
	oldD := n.transportPeer("d", a)
	require.NotNil(t, oldD)
	// Rename b to c with the same key, and give d this node's own key.
	n.CentralCfg.Clients = []state.ClientCfg{client("c", keyB), client("d", localKey)}
	n.CentralCfg.Graph = []string{"node, c", "node, d"}
	require.ErrorContains(t, n.SyncTransport(), "sync neighbour d")
	c := n.transportPeer("c", a)
	require.NotNil(t, c)
	require.Equal(t, "c", c.ID())
	require.Nil(t, n.transportPeer("b", a))
	// d keeps its previous binding until its config is valid again.
	require.Same(t, oldD, n.transportPeer("d", a))
	require.Len(t, a.Peers(), 2)
}

type segmentedTUN struct {
	*testTUN
	failed bool
}

func (h *segmentedTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	if !h.failed {
		h.failed = true
		return 0, tun.ErrTooManySegments
	}
	return h.testTUN.Read(bufs, sizes, offset)
}

func TestTUNReaderSurvivesTooManySegments(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	host := &segmentedTUN{testTUN: &testTUN{input: make(chan []byte, 1), closed: make(chan struct{})}}
	transport := &tunTransport{packets: make(chan []byte, 1)}
	n := &Nylon{Context: ctx, Cancel: cancel, Tun: host, Transports: []polyamide.Transport{transport}, Log: slog.Default()}
	table := new(bart.Table[RouteTableEntry])
	table.Insert(netip.MustParsePrefix("10.0.0.2/32"), RouteTableEntry{Nh: "remote"})
	n.router.Tables.Store(&ForwardingTables{Forward: table, Exit: new(bart.Table[RouteTableEntry]), Links: map[state.NodeId][]Link{"remote": {{Peer: &routingPeer{transport: transport, id: "remote"}}}}})
	n.startTUNReader()
	defer func() { cancel(context.Canceled); _ = host.Close(); n.tunWorkers.Wait() }()
	packet := tuntest.Ping(netip.MustParseAddr("10.0.0.2"), netip.MustParseAddr("10.0.0.1"))
	host.input <- packet
	require.Equal(t, packet, routingReceive(t, transport.packets))
	require.NoError(t, ctx.Err())
}
