package state

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNameValidator_Valid(t *testing.T) {
	assert.NoError(t, NameValidator("1"))
	assert.NoError(t, NameValidator("ab_cd"))
	assert.NoError(t, NameValidator("abcd-a.com"))
}

func TestNameValidator_Invalid(t *testing.T) {
	assert.Error(t, NameValidator("1A"))
	assert.Error(t, NameValidator("node name"))
	assert.Error(t, NameValidator(""))
	assert.Error(t, NameValidator("\t"))
	assert.Error(t, NameValidator("abcd-a.com\\hi"))
	assert.Error(t, NameValidator(strings.Repeat("a", 200)))
}

func TestNodeConfigValidator_DnsResolver(t *testing.T) {
	assert.NoError(t, NodeConfigValidator(nil, &LocalCfg{
		Id:           "valid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"1.1.1.1:53"},
	}))
	assert.NoError(t, NodeConfigValidator(nil, &LocalCfg{
		Id:   "valid-node",
		Port: 5,
		Key:  [32]byte{1},
	}))
	assert.Error(t, NodeConfigValidator(nil, &LocalCfg{
		Id:           "invalid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"google.com"},
	}))
	assert.Error(t, NodeConfigValidator(nil, &LocalCfg{
		Id:           "invalid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"google.com:53"},
	}))
	assert.Error(t, NodeConfigValidator(nil, &LocalCfg{
		Id:           "invalid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"1.1.1.1"},
	}))
}

func TestNodeConfigValidator_RejectsEmptyDistributionKey(t *testing.T) {
	err := NodeConfigValidator(nil, &LocalCfg{
		Id:   "valid-node",
		Port: 5,
		Key:  [32]byte{1},
		Dist: &LocalDistributionCfg{Url: "https://example.com/bundle"},
	})
	assert.ErrorContains(t, err, "dist.key must not be empty")
}

func TestCentralConfigValidator_RejectsEmptyDistributionKey(t *testing.T) {
	err := CentralConfigValidator(&CentralCfg{
		Dist: &DistributionCfg{Repos: []string{"https://example.com/bundle"}},
	})
	assert.ErrorContains(t, err, "dist.key must not be empty")
}

func TestNodeConfigValidator_TunlessMode(t *testing.T) {
	base := LocalCfg{
		Id:    "relay",
		Port:  57175,
		Key:   [32]byte{1},
		NoTun: true,
	}
	assert.NoError(t, NodeConfigValidator(nil, &base))

	base.UseSystemRouting = true
	assert.ErrorContains(t, NodeConfigValidator(nil, &base), "no_tun cannot be used with use_system_routing")

	base.UseSystemRouting = false
	central := CentralCfg{
		Routers: []RouterCfg{{
			NodeCfg: NodeCfg{Id: base.Id},
		}},
	}
	assert.NoError(t, NodeConfigValidator(&central, &base))

	central.Routers[0].Addresses = []netip.Addr{netip.MustParseAddr("10.0.0.1")}
	assert.ErrorContains(t, NodeConfigValidator(&central, &base), "cannot advertise addresses or prefixes")

	central.Routers[0].Addresses = nil
	central.Routers[0].Prefixes = []PrefixHealthWrapper{{
		PrefixHealth: &StaticPrefixHealth{Prefix: netip.MustParsePrefix("192.0.2.0/24")},
	}}
	assert.ErrorContains(t, NodeConfigValidator(&central, &base), "cannot advertise addresses or prefixes")
}

func TestCentralConfigValidator_OverlappingPrefix(t *testing.T) {
	cfg := &CentralCfg{
		Routers: []RouterCfg{
			{
				NodeCfg: NodeCfg{
					Id:     "node1",
					PubKey: NyPublicKey{},
					Prefixes: []PrefixHealthWrapper{
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/32"),
								Metric: 0,
							},
						},
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.0/24"),
								Metric: 0,
							},
						},
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/8"),
								Metric: 0,
							},
						},
					},
				},
			},
		},
	}
	assert.NoError(t, CentralConfigValidator(cfg))
}

func TestCentralConfigValidator_Endpoint(t *testing.T) {
	cfg := &CentralCfg{
		Routers: []RouterCfg{{
			NodeCfg: NodeCfg{Id: "node1"},
			Endpoints: []string{
				"example.com",
				"example.com:57175",
				"192.0.2.1",
				"[2001:db8::1]:57175",
			},
		}},
	}
	assert.NoError(t, CentralConfigValidator(cfg))

	cfg.Routers[0].Endpoints = []string{"http://example.com"}
	assert.ErrorContains(t, CentralConfigValidator(cfg), "invalid endpoint")
}

func TestCentralConfigValidator_PassiveClientNonStaticPrefix(t *testing.T) {
	cfg := &CentralCfg{
		Clients: []ClientCfg{
			{
				NodeCfg: NodeCfg{
					Id: "client1",
					Prefixes: []PrefixHealthWrapper{
						{
							PrefixHealth: &PingPrefixHealth{
								Prefix: netip.MustParsePrefix("10.0.0.0/24"),
								Addr:   netip.MustParseAddr("10.0.0.1"),
							},
						},
					},
				},
			},
		},
	}
	assert.Error(t, CentralConfigValidator(cfg))
	assert.Contains(t, CentralConfigValidator(cfg).Error(), "passive clients may not advertise non-static prefixes")

	cfg.Clients[0].Prefixes[0] = PrefixHealthWrapper{
		PrefixHealth: &StaticPrefixHealth{
			Prefix: netip.MustParsePrefix("10.0.0.0/24"),
			Metric: 0,
		},
	}
	assert.NoError(t, CentralConfigValidator(cfg))
}

func TestCentralConfigValidator_DuplicatePrefix(t *testing.T) {
	cfg := &CentralCfg{
		Routers: []RouterCfg{
			{
				NodeCfg: NodeCfg{
					Id:     "node1",
					PubKey: NyPublicKey{},
					Prefixes: []PrefixHealthWrapper{
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/32"),
								Metric: 0,
							},
						},
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/24"),
								Metric: 0,
							},
						},
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/32"),
								Metric: 0,
							},
						},
					},
				},
			},
		},
	}
	assert.Error(t, CentralConfigValidator(cfg))
}

func TestCentralConfigValidator_AnycastPrefix(t *testing.T) {
	// Anycast routing allows the same prefix to be advertised by multiple nodes
	cfg := &CentralCfg{
		Routers: []RouterCfg{
			{
				NodeCfg: NodeCfg{
					Id:     "node1",
					PubKey: NyPublicKey{},
					Prefixes: []PrefixHealthWrapper{
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/32"),
								Metric: 0,
							},
						},
					},
				},
			},
			{
				NodeCfg: NodeCfg{
					Id:     "node2",
					PubKey: NyPublicKey{},
					Prefixes: []PrefixHealthWrapper{
						{
							&StaticPrefixHealth{
								Prefix: netip.MustParsePrefix("10.5.0.1/32"), // same prefix as node1 - this is valid for anycast
								Metric: 0,
							},
						},
					},
				},
			},
		},
	}
	assert.NoError(t, CentralConfigValidator(cfg))
}
