package core

import (
	"net/netip"
	"runtime"
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

	want := []netip.Prefix{pfx("10.0.0.0/24")}
	if runtime.GOOS == "windows" {
		want = []netip.Prefix{pfx("10.0.0.0/25"), pfx("10.0.0.128/25")}
	}
	assert.Equal(t, want, sortedPrefixes(n.ComputeSysRouteTable()))
}

func TestSystemRoutePrefixesPlatformAggregation(t *testing.T) {
	for _, tc := range []struct {
		name               string
		prefixes, excluded []netip.Prefix
		merged, separate   []netip.Prefix
	}{
		{
			name:     "adjacent hosts",
			prefixes: []netip.Prefix{pfx("10.0.0.3/32"), pfx("10.0.0.2/32")},
			merged:   []netip.Prefix{pfx("10.0.0.2/31")},
			separate: []netip.Prefix{pfx("10.0.0.2/32"), pfx("10.0.0.3/32")},
		},
		{
			name:     "four adjacent hosts",
			prefixes: []netip.Prefix{pfx("10.0.0.4/32"), pfx("10.0.0.5/32"), pfx("10.0.0.6/32"), pfx("10.0.0.7/32")},
			merged:   []netip.Prefix{pfx("10.0.0.4/30")},
			separate: []netip.Prefix{pfx("10.0.0.4/32"), pfx("10.0.0.5/32"), pfx("10.0.0.6/32"), pfx("10.0.0.7/32")},
		},
		{
			name:     "adjacent subnets",
			prefixes: []netip.Prefix{pfx("10.0.0.0/25"), pfx("10.0.0.128/25")},
			merged:   []netip.Prefix{pfx("10.0.0.0/24")},
			separate: []netip.Prefix{pfx("10.0.0.0/25"), pfx("10.0.0.128/25")},
		},
		{
			name:     "explicit subnet stays intact",
			prefixes: []netip.Prefix{pfx("10.0.0.2/31")},
			merged:   []netip.Prefix{pfx("10.0.0.2/31")},
			separate: []netip.Prefix{pfx("10.0.0.2/31")},
		},
		{
			name:     "IPv6 hosts",
			prefixes: []netip.Prefix{pfx("fd00::2/128"), pfx("fd00::3/128")},
			merged:   []netip.Prefix{pfx("fd00::2/127")},
			separate: []netip.Prefix{pfx("fd00::2/128"), pfx("fd00::3/128")},
		},
		{
			name:     "exclusions split an advertised subnet",
			prefixes: []netip.Prefix{pfx("10.0.0.0/24")},
			excluded: []netip.Prefix{pfx("10.0.0.64/26")},
			merged:   []netip.Prefix{pfx("10.0.0.0/26"), pfx("10.0.0.128/25")},
			separate: []netip.Prefix{pfx("10.0.0.0/26"), pfx("10.0.0.128/25")},
		},
		{
			name:     "overlap after exclusions is deduplicated",
			prefixes: []netip.Prefix{pfx("10.0.0.0/24"), pfx("10.0.0.0/25")},
			excluded: []netip.Prefix{pfx("10.0.0.128/25")},
			merged:   []netip.Prefix{pfx("10.0.0.0/25")},
			separate: []netip.Prefix{pfx("10.0.0.0/25")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := slices.Clone(tc.prefixes)
			for _, goos := range []string{"linux", "darwin", "windows"} {
				want := tc.merged
				if goos == "windows" {
					want = tc.separate
				}
				assert.Equal(t, want, systemRoutePrefixes(tc.prefixes, state.MakeSet(tc.excluded), goos), goos)
			}
			assert.Equal(t, original, tc.prefixes, "selected prefixes must not be mutated")
		})
	}
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
