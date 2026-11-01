//go:build integration

package integration

import (
	"fmt"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyCentralConfigRemovesNeighbourFromLiveNode(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		a1, b1, c1 := "192.168.10.1:1234", "192.168.10.2:1234", "192.168.10.3:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.AddNode("c", "10.0.0.3/32", c1)
		h.Central.Graph = []string{"a, b, c"}
		h.Connect(a1, b1)
		h.Connect(a1, c1)
		h.Connect(b1, c1)
		h.Start()

		next := h.Central
		next.Timestamp++
		next.Graph = []string{
			"a, c",
			"b, c",
		}
		bKey := h.Router("b").PubKey
		cKey := h.Router("c").PubKey

		apply := OnNode(h, "a", func(n *core.Nylon) applyResult {
			beforeC := n.RouterState.GetNeighbour("c")
			if beforeC == nil || len(beforeC.Eps) == 0 {
				return applyResult{Err: fmt.Errorf("expected neighbour c with at least one endpoint before apply")}
			}
			keptEndpoint := beforeC.Eps[0]
			result, err := n.ApplyCentralConfig(&next)
			afterC := n.RouterState.GetNeighbour("c")
			return applyResult{
				Result:            result,
				Err:               err,
				HasBNeighbour:     n.RouterState.GetNeighbour("b") != nil,
				HasCNeighbour:     afterC != nil,
				HasBAppliedPeer:   hasAppliedPeer(n, "b"),
				HasCAppliedPeer:   hasAppliedPeer(n, "c"),
				HasBWGPeer:        hasWGPeer(n, bKey),
				HasCWGPeer:        hasWGPeer(n, cKey),
				PreservedEndpoint: afterC != nil && len(afterC.Eps) > 0 && afterC.Eps[0] == keptEndpoint,
			}
		})

		assert.NoError(t, apply.Err)
		assert.Equal(t, core.ApplyApplied, apply.Result)
		assert.False(t, apply.HasBNeighbour)
		assert.True(t, apply.HasCNeighbour)
		assert.False(t, apply.HasBAppliedPeer)
		assert.True(t, apply.HasCAppliedPeer)
		assert.False(t, apply.HasBWGPeer)
		assert.True(t, apply.HasCWGPeer)
		assert.True(t, apply.PreservedEndpoint)
	})
}

func TestApplyCentralConfigLocalNodeRemovedRequiresRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		a1, b1 := "192.168.20.1:1234", "192.168.20.2:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.Central.Graph = []string{"a, b"}
		h.Connect(a1, b1)
		h.Start()

		next := h.Central
		next.Timestamp++
		next.Routers = next.Routers[1:]
		next.Graph = nil

		type outcome struct {
			result           core.ApplyResult
			err              error
			centralUnchanged bool
			bStillNeighbour  bool
		}
		got := OnNode(h, "a", func(n *core.Nylon) outcome {
			result, err := n.ApplyCentralConfig(&next)
			return outcome{
				result:           result,
				err:              err,
				centralUnchanged: n.CentralCfg.Timestamp == h.Central.Timestamp,
				bStillNeighbour:  n.RouterState.GetNeighbour("b") != nil,
			}
		})

		assert.Equal(t, core.ApplyRestartRequired, got.result)
		assert.Error(t, got.err)
		assert.True(t, got.centralUnchanged)
		assert.True(t, got.bStillNeighbour)
	})
}

func TestApplyCentralConfigRotatesPeerKeyWithoutChangingNextHop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		h.UntrackedRouting = true
		a1, b1 := "192.168.30.1:1234", "192.168.30.2:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.Central.Graph = []string{"a, b"}
		h.Connect(a1, b1)

		received := make(chan byte, 16)
		bAddr := netip.MustParseAddr("10.0.0.2")
		h.OnDeliver(func(node state.NodeId, _, dst netip.Addr, data []byte) bool {
			if node == "b" && dst == bAddr && len(data) != 0 {
				select {
				case received <- data[0]:
				default:
				}
			}
			return true
		})
		h.Start()

		waitForPayload(h, received, 1)

		newPrivateKey := state.GenerateKey()
		err, next := h.Central.Clone()
		require.NoError(t, err)
		next.Timestamp++
		routerOf(next, "b").PubKey = newPrivateKey.Pubkey()

		// A local identity is fixed at construction. Restart the remote node
		// while keeping the sender alive so its forwarding table must rebind
		// the same hop.
		h.RestartNode("b", *next, func(local *state.LocalCfg) { local.Key = newPrivateKey })
		h.ApplyConfig("a", next)

		waitForPayload(h, received, 2)
	})
}

func TestApplyCentralConfigRemovedPrefixStopsSeqnoRequests(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		a1, b1 := "192.168.40.1:1234", "192.168.40.2:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.Central.Graph = []string{"a, b"}
		h.Connect(a1, b1)
		removed := netip.MustParsePrefix("10.1.0.2/32")
		h.Router("b").Prefixes = append(h.Router("b").Prefixes, state.PrefixHealthWrapper{
			PrefixHealth: &state.StaticPrefixHealth{Prefix: removed},
		})
		h.Start()

		h.Eventually(10*time.Second, "a to learn the prefix", func() bool {
			return OnNode(h, "a", func(n *core.Nylon) bool {
				route, ok := n.RouterState.GetNeighbour("b").Routes[removed]
				return ok && route.Metric != state.INF
			})
		})

		err, next := h.Central.Clone()
		require.NoError(t, err)
		next.Timestamp++
		bNext := routerOf(next, "b")
		bNext.Prefixes = bNext.Prefixes[:1]
		h.ApplyConfig("b", next)
		h.ApplyConfig("a", next)

		// a stale route would keep a requesting seqnos until it expires
		stale := OnNode(h, "a", func(n *core.Nylon) bool {
			_, ok := n.RouterState.GetNeighbour("b").Routes[removed]
			return ok
		})
		assert.False(t, stale)
	})
}

// waitForPayload keeps sending payload from a to b until b receives it.
func waitForPayload(h *Harness, received <-chan byte, payload byte) {
	h.t.Helper()
	stop := h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.2", []byte{payload}, 64)
	defer stop()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case got := <-received:
			if got == payload {
				return
			}
		case <-deadline:
			h.t.Fatalf("timed out waiting for payload %d", payload)
		case <-h.ctx.Done():
			h.t.Fatalf("harness stopped while waiting for payload %d", payload)
		}
	}
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
	for _, transport := range n.Transports {
		for _, stat := range transport.Peers() {
			if [32]byte(stat.Peer.PublicKey()) == [32]byte(key) {
				return true
			}
		}
	}
	return false
}

// routerOf returns id's entry in cfg.
func routerOf(cfg *state.CentralCfg, id state.NodeId) *state.RouterCfg {
	for i := range cfg.Routers {
		if cfg.Routers[i].Id == id {
			return &cfg.Routers[i]
		}
	}
	panic("unknown router " + string(id))
}
