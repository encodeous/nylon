package core

import (
	"net/netip"
	"testing"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

// TestComputeSysRouteTable_ExitNodeAddsDefault verifies that a node
// configured with an exit node installs 0.0.0.0/0 (so the OS hands all
// unrouted traffic to nylon), while still respecting both the central
// excludes and the default-local exclude list (loopback, multicast,
// link-local).
func TestComputeSysRouteTable_ExitNodeAddsDefault(t *testing.T) {
	r := &Nylon{
		ConfigState: state.ConfigState{
			LocalCfg: state.LocalCfg{Id: "node-a"},
		},
		RouterState: &state.RouterState{
			Id: "node-a",
			Routes: map[netip.Prefix]state.SelRoute{
				netip.MustParsePrefix("10.0.0.2/32"): {
					PubRoute: state.PubRoute{
						Source: state.Source{
							NodeId: "node-b",
							Prefix: netip.MustParsePrefix("10.0.0.2/32"),
						},
					},
					Nh: "node-b",
				},
			},
		},
	}

	// without an exit node configured: just the overlay prefix.
	assert.ElementsMatch(t, []netip.Prefix{
		netip.MustParsePrefix("10.0.0.2/32"),
	}, r.ComputeSysRouteTable())

	r.LocalCfg.ExitNode = "node-exit"
	r.LocalCfg.ExcludeIPs = []netip.Prefix{netip.MustParsePrefix("192.168.0.0/16")}
	routes := r.ComputeSysRouteTable()

	assert.True(t, prefixListContainsAddr(routes, netip.MustParseAddr("10.0.0.2")))
	assert.NotContains(t, routes, netip.MustParsePrefix("192.168.0.0/16"))
	for _, route := range routes {
		assert.False(t, route.Contains(netip.MustParseAddr("192.168.1.1")), route.String())
		assert.False(t, route.Contains(netip.MustParseAddr("224.0.0.251")), route.String())
		assert.False(t, route.Contains(netip.MustParseAddr("169.254.1.1")), route.String())
	}
	// must still cover a public address — i.e. the default route is in
	// fact present.
	assert.True(t, prefixListContainsAddr(routes, netip.MustParseAddr("8.8.8.8")))
}

func prefixListContainsAddr(prefixes []netip.Prefix, addr netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
