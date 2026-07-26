package core

import (
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/state"
	"github.com/gaissmai/bart"
	"github.com/stretchr/testify/assert"
)

func TestReconcileAdvertisedPrefixesStartsChangedPrefixHealth(t *testing.T) {
	prefix := netip.MustParsePrefix("fd00::53/128")
	oldPrefix := state.PrefixHealthWrapper{
		PrefixHealth: &state.StaticPrefixHealth{Prefix: prefix},
	}
	n := testNylonWithPrefixes(oldPrefix)
	n.RouterState.Advertised[prefix] = state.Advertisement{
		NodeId:   n.LocalCfg.Id,
		Expiry:   maxConfigTime,
		MetricFn: func() uint32 { return 0 },
	}

	delay := time.Millisecond
	next := testCentralConfig(n.LocalCfg.Id, state.PrefixHealthWrapper{
		PrefixHealth: &state.HTTPPrefixHealth{
			Prefix: prefix,
			URL:    "http://127.0.0.1:1/healthz",
			Delay:  &delay,
		},
	})

	n.reconcileAdvertisedPrefixes(&next)
	t.Cleanup(n.prefixHealth[prefix].monitor.Stop)

	assert.Equal(t, state.INF, n.RouterState.Advertised[prefix].MetricFn())
}

func TestReconcileAdvertisedPrefixesStartsChangedPingPrefixHealth(t *testing.T) {
	prefix := netip.MustParsePrefix("fd00::54/128")
	oldPrefix := state.PrefixHealthWrapper{
		PrefixHealth: &state.StaticPrefixHealth{Prefix: prefix},
	}
	n := testNylonWithPrefixes(oldPrefix)
	n.RouterState.Advertised[prefix] = state.Advertisement{
		NodeId:   n.LocalCfg.Id,
		Expiry:   maxConfigTime,
		MetricFn: func() uint32 { return 0 },
	}

	delay := 100 * time.Millisecond
	next := testCentralConfig(n.LocalCfg.Id, state.PrefixHealthWrapper{
		PrefixHealth: &state.PingPrefixHealth{
			Prefix: prefix,
			Addr:   netip.MustParseAddr("127.0.0.1"),
			Delay:  &delay,
		},
	})

	n.reconcileAdvertisedPrefixes(&next)
	t.Cleanup(n.prefixHealth[prefix].monitor.Stop)

	assert.Equal(t, state.INF, n.RouterState.Advertised[prefix].MetricFn())
}

func TestReconcileAdvertisedPrefixesReusesUnchangedMonitor(t *testing.T) {
	prefix := netip.MustParsePrefix("fd00::53/128")
	delay := time.Millisecond
	current := state.PrefixHealthWrapper{
		PrefixHealth: &state.HTTPPrefixHealth{
			Prefix: prefix,
			URL:    "http://127.0.0.1:1/healthz",
			Delay:  &delay,
		},
	}
	n := testNylonWithPrefixes(current)
	monitor := current.NewMonitor(n.Log, &n.RouterTunables, n.DNSResolver)
	t.Cleanup(monitor.Stop)
	n.prefixHealth = map[netip.Prefix]advertisedPrefixHealth{
		prefix: {
			config:  current,
			monitor: monitor,
		},
	}
	n.RouterState.Advertised[prefix] = state.Advertisement{
		NodeId:   n.LocalCfg.Id,
		Expiry:   maxConfigTime,
		MetricFn: monitor.GetMetric,
		ExpiryFn: monitor.Stop,
	}

	next := testCentralConfig(n.LocalCfg.Id, state.PrefixHealthWrapper{
		PrefixHealth: &state.HTTPPrefixHealth{
			Prefix: prefix,
			URL:    "http://127.0.0.1:1/healthz",
			Delay:  &delay,
		},
	})

	n.reconcileAdvertisedPrefixes(&next)

	assert.Equal(t, monitor, n.prefixHealth[prefix].monitor)
	assert.Equal(t, state.INF, n.RouterState.Advertised[prefix].MetricFn())
}

func TestRebindForwardingPeersUpdatesUnchangedNextHop(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	nextHop := state.NodeId("next")
	oldPeer := new(device.Peer)
	newPeer := new(device.Peer)
	forward := new(bart.Table[RouteTableEntry])
	forward.Insert(prefix, RouteTableEntry{Nh: nextHop, Peer: oldPeer})

	rebound, changed := rebindRouteTablePeers(forward, map[state.NodeId]*device.Peer{
		nextHop: newPeer,
	})

	assert.True(t, changed)
	forwardEntry, ok := rebound.Lookup(prefix.Addr())
	if assert.True(t, ok) {
		assert.Same(t, newPeer, forwardEntry.Peer)
		assert.Equal(t, nextHop, forwardEntry.Nh)
	}
	oldEntry, ok := forward.Lookup(prefix.Addr())
	if assert.True(t, ok) {
		assert.Same(t, oldPeer, oldEntry.Peer, "published forwarding snapshots must remain immutable")
	}
}

func TestRebindForwardingPeersReusesUnchangedTable(t *testing.T) {
	prefix := netip.MustParsePrefix("10.0.0.0/24")
	nextHop := state.NodeId("next")
	peer := new(device.Peer)
	forward := new(bart.Table[RouteTableEntry])
	forward.Insert(prefix, RouteTableEntry{Nh: nextHop, Peer: peer})

	rebound, changed := rebindRouteTablePeers(forward, map[state.NodeId]*device.Peer{
		nextHop: peer,
	})

	assert.False(t, changed)
	assert.Same(t, forward, rebound)
}

func TestHandleNylonPacketDropsUnknownRetiredPeer(t *testing.T) {
	n := &Nylon{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	peerMap := make(map[state.NyPublicKey]state.NodeId)
	n.PeerMap.Store(&peerMap)

	assert.NotPanics(t, func() {
		n.handleNylonPacket(nil, nil, new(device.Peer))
	})
}

func testNylonWithPrefixes(prefixes ...state.PrefixHealthWrapper) *Nylon {
	id := state.NodeId("node")
	tunables := state.DefaultRouterTunables()
	return &Nylon{
		RouterTunables: tunables,
		ConfigState: state.ConfigState{
			CentralCfg: testCentralConfig(id, prefixes...),
			LocalCfg: state.LocalCfg{
				Id: id,
			},
		},
		RouterState: &state.RouterState{
			RouterTunables: &tunables,
			Id:             id,
			SelfSeqno:      make(map[netip.Prefix]uint16),
			Routes:         make(map[netip.Prefix]state.SelRoute),
			Sources:        make(map[state.Source]state.FD),
			Advertised:     make(map[netip.Prefix]state.Advertisement),
		},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func testCentralConfig(id state.NodeId, prefixes ...state.PrefixHealthWrapper) state.CentralCfg {
	return state.CentralCfg{
		Routers: []state.RouterCfg{
			{
				NodeCfg: state.NodeCfg{
					Id:       id,
					Prefixes: prefixes,
				},
			},
		},
	}
}
