package state

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildNodeIdMap_DeterministicAcrossOrdering(t *testing.T) {
	cfgA := &CentralCfg{
		Routers: []RouterCfg{
			{NodeCfg: NodeCfg{Id: "syd-vm"}},
			{NodeCfg: NodeCfg{Id: "ios-phone"}},
			{NodeCfg: NodeCfg{Id: "openstick"}},
		},
		Clients: []ClientCfg{
			{NodeCfg: NodeCfg{Id: "tablet"}},
		},
	}
	cfgB := &CentralCfg{
		Routers: []RouterCfg{
			{NodeCfg: NodeCfg{Id: "openstick"}},
			{NodeCfg: NodeCfg{Id: "syd-vm"}},
			{NodeCfg: NodeCfg{Id: "ios-phone"}},
		},
		Clients: []ClientCfg{
			{NodeCfg: NodeCfg{Id: "tablet"}},
		},
	}
	mA, err := BuildNodeIdMap(cfgA)
	assert.NoError(t, err)
	mB, err := BuildNodeIdMap(cfgB)
	assert.NoError(t, err)

	for _, id := range []NodeId{"syd-vm", "ios-phone", "openstick", "tablet"} {
		a, ok := mA.ToBin(id)
		assert.True(t, ok)
		b, ok := mB.ToBin(id)
		assert.True(t, ok)
		assert.Equal(t, a, b, "binary id for %s should be stable across input order", id)
	}
}

func TestBuildNodeIdMap_ReservesZero(t *testing.T) {
	cfg := &CentralCfg{
		Routers: []RouterCfg{{NodeCfg: NodeCfg{Id: "a"}}},
	}
	m, err := BuildNodeIdMap(cfg)
	assert.NoError(t, err)

	_, ok := m.ToString(InvalidNodeIdBin)
	assert.False(t, ok, "InvalidNodeIdBin must not resolve to a node")

	bin, ok := m.ToBin("a")
	assert.True(t, ok)
	assert.NotEqual(t, InvalidNodeIdBin, bin)

	_, ok = m.ToBin("missing")
	assert.False(t, ok)
}
