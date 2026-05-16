//go:build integration

package integration

import (
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"go.uber.org/goleak"
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
	defer goleak.VerifyNone(t)
	tunables := state.DefaultRouterTunables()
	tunables.ProbeDelay /= 3
	tunables.RouteUpdateDelay = 500 * time.Millisecond
	tunables.NeighbourIOFlushDelay = 100 * time.Millisecond

	vh := &VirtualHarness{}
	vh.Tunables = &tunables
	a1 := "192.168.1.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.1.2:1234"
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

	bPrefix := netip.MustParsePrefix("10.0.0.2/32")
	na := vh.Nylons[0].Load()
	nb := vh.Nylons[1].Load()

	// Wait for routes to converge: a should have a route to b's prefix
	converged := make(chan struct{})
	go func() {
		for {
			select {
			case <-vh.Context.Done():
				return
			case <-time.After(50 * time.Millisecond):
				na.Dispatch(func() error {
					route, ok := na.RouterState.Routes[bPrefix]
					if ok && route.Metric != state.INF {
						select {
						case <-converged:
						default:
							close(converged)
						}
					}
					return nil
				})
			}
		}
	}()

	select {
	case <-converged:
		t.Log("Routes converged")
	case <-time.After(30 * time.Second):
		t.Fatal("Timed out waiting for route convergence")
	case err := <-errs:
		t.Fatal(err)
	}

	// B retracts its prefix: remove from Advertised and broadcast INF.
	nb.Dispatch(func() error {
		delete(nb.RouterState.Advertised, bPrefix)
		nb.BroadcastSendRouteUpdate(state.PubRoute{
			Source: state.Source{NodeId: "b", Prefix: bPrefix},
			FD:     state.FD{Seqno: nb.RouterState.GetSeqno(bPrefix), Metric: state.INF},
		})
		return nil
	})

	// If ACKs are sent on wire, B will receive HandleAckRetract from A,
	// populating RetractedBy. Since A is B's only neighbour, the held route
	// will be released.
	ackReceived := make(chan struct{})
	go func() {
		for {
			select {
			case <-vh.Context.Done():
				return
			case <-time.After(50 * time.Millisecond):
				nb.Dispatch(func() error {
					route, ok := nb.RouterState.Routes[bPrefix]
					if !ok || len(route.RetractedBy) > 0 {
						select {
						case <-ackReceived:
						default:
							close(ackReceived)
						}
					}
					return nil
				})
			}
		}
	}()

	select {
	case <-ackReceived:
		t.Log("ACK retract received on wire - held route released")
	case <-time.After(5 * time.Second):
		t.Fatal("ACK retract was never received on wire - bug: flushIO does not send ACKs")
	case err := <-errs:
		t.Fatal(err)
	}

	vh.Stop()
}
