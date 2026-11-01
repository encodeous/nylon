//go:build integration

package integration

import (
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/state"
)

// TestAckRetractSentOnWire verifies that ACK retract messages are actually
// serialized and sent over the wire (not just queued in memory).
//
// Setup: A -- B, where B advertises 10.0.0.2/32.
// B retracts its route (removes advertisement + broadcasts INF).
// A should respond with an ACK retract on the wire.
// If ACKs are sent correctly, B's RetractedBy will be populated and the
// held route (blackhole) released quickly.
func TestAckRetractSentOnWire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tunables := state.DefaultRouterTunables()
		tunables.ProbeDelay /= 3
		tunables.RouteUpdateDelay = 500 * time.Millisecond
		tunables.NeighbourIOFlushDelay = 100 * time.Millisecond

		h := NewHarness(t)
		h.Tunables = &tunables
		a1, b1 := "192.168.1.1:1234", "192.168.1.2:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.Central.Graph = []string{"a, b"}
		h.Connect(a1, b1)
		h.Start()

		bPrefix := netip.MustParsePrefix("10.0.0.2/32")
		h.Eventually(30*time.Second, "a to learn a route to b", func() bool {
			return OnNode(h, "a", func(n *core.Nylon) bool {
				route, ok := n.RouterState.Routes[bPrefix]
				return ok && route.Metric != state.INF
			})
		})

		// B retracts its prefix: remove from Advertised and broadcast INF.
		OnNode(h, "b", func(n *core.Nylon) struct{} {
			delete(n.RouterState.Advertised, bPrefix)
			n.BroadcastSendRouteUpdate(state.PubRoute{
				Source: state.Source{NodeId: "b", Prefix: bPrefix},
				FD:     state.FD{Seqno: n.RouterState.GetSeqno(bPrefix), Metric: state.INF},
			})
			return struct{}{}
		})

		// If ACKs are sent on wire, B will receive HandleAckRetract from A,
		// populating RetractedBy. Since A is B's only neighbour, the held route
		// will be released.
		h.Eventually(5*time.Second, "b to receive an ACK retract on the wire", func() bool {
			return OnNode(h, "b", func(n *core.Nylon) bool {
				route, ok := n.RouterState.Routes[bPrefix]
				return !ok || len(route.RetractedBy) > 0
			})
		})
	})
}
