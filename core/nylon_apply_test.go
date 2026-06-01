package core

import (
	"io"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
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
		MetricFn: oldPrefix.GetMetric,
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
	t.Cleanup(next.Routers[0].Prefixes[0].Stop)

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
		MetricFn: oldPrefix.GetMetric,
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
	t.Cleanup(next.Routers[0].Prefixes[0].Stop)

	assert.Equal(t, state.INF, n.RouterState.Advertised[prefix].MetricFn())
}

func TestReconcileAdvertisedPrefixesReusesUnchangedRunningPrefixHealth(t *testing.T) {
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
	current.Start(n.Log, &n.RouterTunables)
	t.Cleanup(current.Stop)
	n.RouterState.Advertised[prefix] = state.Advertisement{
		NodeId:   n.LocalCfg.Id,
		Expiry:   maxConfigTime,
		MetricFn: current.GetMetric,
		ExpiryFn: current.Stop,
	}

	next := testCentralConfig(n.LocalCfg.Id, state.PrefixHealthWrapper{
		PrefixHealth: &state.HTTPPrefixHealth{
			Prefix: prefix,
			URL:    "http://127.0.0.1:1/healthz",
			Delay:  &delay,
		},
	})

	n.reconcileAdvertisedPrefixes(&next)

	assert.Same(t, current.PrefixHealth, next.Routers[0].Prefixes[0].PrefixHealth)
	assert.Equal(t, state.INF, n.RouterState.Advertised[prefix].MetricFn())
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

func TestConfiguredEndpointsExpandsBindsAcrossMatchingEndpointFamilies(t *testing.T) {
	tunables := state.DefaultRouterTunables()
	eps := configuredEndpoints([]state.LocalBind{
		{Source: netip.MustParseAddr("192.0.2.10")},
		{Source: netip.MustParseAddr("2001:db8::10")},
	}, []*state.DynamicEndpoint{
		state.NewDynamicEndpoint("198.51.100.10:57175"),
		state.NewDynamicEndpoint("[2001:db8::20]:57175"),
	}, &tunables)

	assert.Len(t, eps, 2)
	assert.Equal(t, netip.MustParseAddr("192.0.2.10"), eps[0].AsNylonEndpoint().Bind.Source)
	assert.Equal(t, "198.51.100.10:57175", eps[0].AsNylonEndpoint().DynEP.Value)
	assert.Equal(t, netip.MustParseAddr("2001:db8::10"), eps[1].AsNylonEndpoint().Bind.Source)
	assert.Equal(t, "[2001:db8::20]:57175", eps[1].AsNylonEndpoint().DynEP.Value)
}

func TestConfiguredEndpointsUsesDefaultEndpointWhenNoBindsConfigured(t *testing.T) {
	tunables := state.DefaultRouterTunables()
	eps := configuredEndpoints(nil, []*state.DynamicEndpoint{
		state.NewDynamicEndpoint("198.51.100.10:57175"),
		state.NewDynamicEndpoint("[2001:db8::20]:57175"),
	}, &tunables)

	assert.Len(t, eps, 2)
	assert.False(t, eps[0].AsNylonEndpoint().Bind.Source.IsValid())
	assert.False(t, eps[1].AsNylonEndpoint().Bind.Source.IsValid())
}
