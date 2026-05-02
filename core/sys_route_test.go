package core

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

func TestComputeSysRouteTableAppliesExcludesAndUnexcludes(t *testing.T) {
	n := sysRouteTestNylon(
		"a",
		[]netip.Prefix{
			pfx("10.0.0.128/25"),
		},
		[]netip.Prefix{
			pfx("10.0.1.64/26"),
		},
		[]netip.Prefix{
			pfx("10.0.2.0/25"),
		},
		map[netip.Prefix]state.SelRoute{
			pfx("10.0.0.0/24"): {Nh: "b"},
			pfx("10.0.1.0/24"): {Nh: "a"},
			pfx("10.0.2.0/24"): {Nh: "c"},
		},
	)

	assert.ElementsMatch(t, []netip.Prefix{
		pfx("10.0.0.0/25"),
		pfx("10.0.1.64/26"),
		pfx("10.0.2.128/25"),
	}, n.ComputeSysRouteTable())
}

func TestComputeSysRouteTableLocalExcludeOverridesUnexclude(t *testing.T) {
	n := sysRouteTestNylon(
		"a",
		[]netip.Prefix{
			pfx("10.0.0.0/24"),
		},
		[]netip.Prefix{
			pfx("10.0.0.0/25"),
		},
		[]netip.Prefix{
			pfx("10.0.0.64/26"),
		},
		map[netip.Prefix]state.SelRoute{
			pfx("10.0.0.0/24"): {Nh: "b"},
		},
	)

	assert.ElementsMatch(t, []netip.Prefix{
		pfx("10.0.0.0/26"),
	}, n.ComputeSysRouteTable())
}

func TestComputeSysRouteTableDoesNotMutateCentralExcludes(t *testing.T) {
	centralExcludes := make([]netip.Prefix, 1, 4)
	centralExcludes[0] = pfx("10.0.0.0/25")
	n := sysRouteTestNylon(
		"a",
		centralExcludes,
		nil,
		nil,
		map[netip.Prefix]state.SelRoute{
			pfx("10.0.1.0/24"): {Nh: "a"},
		},
	)

	_ = n.ComputeSysRouteTable()

	assert.Equal(t, []netip.Prefix{pfx("10.0.0.0/25")}, n.CentralCfg.ExcludeIPs)
}

func TestComputeSysRouteTableCoalescesAdjacentResults(t *testing.T) {
	n := sysRouteTestNylon(
		"a",
		nil,
		nil,
		nil,
		map[netip.Prefix]state.SelRoute{
			pfx("10.0.0.0/25"):   {Nh: "b"},
			pfx("10.0.0.128/25"): {Nh: "b"},
		},
	)

	assert.Equal(t, []netip.Prefix{pfx("10.0.0.0/24")}, sortedPrefixes(n.ComputeSysRouteTable()))
}

func sysRouteTestNylon(local state.NodeId, centralExcludes, localUnexcludes, localExcludes []netip.Prefix, routes map[netip.Prefix]state.SelRoute) *Nylon {
	return &Nylon{
		ConfigState: state.ConfigState{
			CentralCfg: state.CentralCfg{
				ExcludeIPs: centralExcludes,
			},
			LocalCfg: state.LocalCfg{
				Id:           local,
				UnexcludeIPs: localUnexcludes,
				ExcludeIPs:   localExcludes,
			},
		},
		RouterState: &state.RouterState{
			Routes: routes,
		},
	}
}

func pfx(s string) netip.Prefix {
	return netip.MustParsePrefix(s)
}

func sortedPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	slices.SortFunc(prefixes, func(a, b netip.Prefix) int {
		return a.Compare(b)
	})
	return prefixes
}
