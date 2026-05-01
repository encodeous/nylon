//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
)

func TestApplyCentralConfigRemovesNeighbourFromLiveNode(t *testing.T) {
	defer goleak.VerifyNone(t)

	vh := &VirtualHarness{}
	a1 := "192.168.10.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.10.2:1234"
	vh.NewNode("b", "10.0.0.2/32")
	c1 := "192.168.10.3:1234"
	vh.NewNode("c", "10.0.0.3/32")
	vh.Central.Graph = []string{
		"a, b, c",
	}
	vh.Endpoints = map[string]state.NodeId{
		a1: "a",
		b1: "b",
		c1: "c",
	}
	vh.AddLink(a1, b1)
	vh.AddLink(b1, a1)
	vh.AddLink(a1, c1)
	vh.AddLink(c1, a1)
	vh.AddLink(b1, c1)
	vh.AddLink(c1, b1)

	errs := vh.Start()
	defer vh.Stop()

	var apply applyResult
	done := make(chan struct{})
	next := vh.Central
	next.Timestamp++
	next.Graph = []string{
		"a, c",
		"b, c",
	}

	a := vh.Nylons[vh.IndexOf("a")]
	a.Dispatch(func() error {
		beforeC := a.RouterState.GetNeighbour("c")
		if beforeC == nil || len(beforeC.Eps) == 0 {
			apply.Err = fmt.Errorf("expected neighbour c with at least one endpoint before apply")
			close(done)
			return nil
		}
		keptEndpoint := beforeC.Eps[0]
		result, err := a.ApplyCentralConfig(next)
		afterC := a.RouterState.GetNeighbour("c")

		apply = applyResult{
			Result:            result,
			Err:               err,
			HasBNeighbour:     a.RouterState.GetNeighbour("b") != nil,
			HasCNeighbour:     afterC != nil,
			HasBAppliedPeer:   hasAppliedPeer(a, "b"),
			HasCAppliedPeer:   hasAppliedPeer(a, "c"),
			HasBWGPeer:        hasWGPeer(a, vh.Central.GetNode("b").PubKey),
			HasCWGPeer:        hasWGPeer(a, vh.Central.GetNode("c").PubKey),
			PreservedEndpoint: afterC != nil && len(afterC.Eps) > 0 && afterC.Eps[0] == keptEndpoint,
		}
		close(done)
		return nil
	})

	select {
	case <-done:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for central config apply")
	}

	assert.NoError(t, apply.Err)
	assert.Equal(t, core.ApplyApplied, apply.Result)
	assert.False(t, apply.HasBNeighbour)
	assert.True(t, apply.HasCNeighbour)
	assert.False(t, apply.HasBAppliedPeer)
	assert.True(t, apply.HasCAppliedPeer)
	assert.False(t, apply.HasBWGPeer)
	assert.True(t, apply.HasCWGPeer)
	assert.True(t, apply.PreservedEndpoint)
}

func TestApplyCentralConfigLocalNodeRemovedRequiresRestart(t *testing.T) {
	defer goleak.VerifyNone(t)

	vh := &VirtualHarness{}
	a1 := "192.168.20.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.20.2:1234"
	vh.NewNode("b", "10.0.0.2/32")
	vh.Central.Graph = []string{
		"a, b",
	}
	vh.Endpoints = map[string]state.NodeId{
		a1: "a",
		b1: "b",
	}
	vh.AddLink(a1, b1)
	vh.AddLink(b1, a1)

	errs := vh.Start()
	defer vh.Stop()

	a := vh.Nylons[vh.IndexOf("a")]
	next := vh.Central
	next.Timestamp++
	next.Routers = next.Routers[1:]
	next.Graph = nil

	done := make(chan struct{})
	var result core.ApplyResult
	var err error
	var centralUnchanged bool
	var bStillNeighbour bool
	a.Dispatch(func() error {
		result, err = a.ApplyCentralConfig(next)
		centralUnchanged = a.CentralCfg.Timestamp == vh.Central.Timestamp
		bStillNeighbour = a.RouterState.GetNeighbour("b") != nil
		close(done)
		return nil
	})

	select {
	case <-done:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for rejected central config apply")
	}

	assert.Equal(t, core.ApplyRestartRequired, result)
	assert.Error(t, err)
	assert.True(t, centralUnchanged)
	assert.True(t, bStillNeighbour)
}

type applyResult struct {
	Result            core.ApplyResult
	Err               error
	HasBNeighbour     bool
	HasCNeighbour     bool
	HasBAppliedPeer   bool
	HasCAppliedPeer   bool
	HasBWGPeer        bool
	HasCWGPeer        bool
	PreservedEndpoint bool
}

func hasAppliedPeer(n *core.Nylon, id state.NodeId) bool {
	_, ok := n.AppliedSystem.Peers[id]
	return ok
}

func hasWGPeer(n *core.Nylon, key state.NyPublicKey) bool {
	return n.Device.LookupPeer(device.NoisePublicKey(key)) != nil
}
