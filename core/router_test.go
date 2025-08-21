//go:build router_test

package core

import (
	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestBasicComputeRoutes(t *testing.T) {
	h := &RouterHarness{}
	rs := state.RouterState{
		Id:         "a",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("b", "c", "d"),
		Advertised: []state.ServiceId{"a"},
	}
	ComputeRoutes(&rs, h)
	// we should have only routes to ourselves
	if len(rs.Routes) != 1 {
		t.Errorf("Expected 1 route, got %d", len(rs.Routes))
	}
	if _, ok := rs.Routes[("a")]; !ok {
		t.Errorf("Expected route to service 'a', but it was not found")
	}
	out := h.GetActions()
	out.AssertContains(t, "BROADCAST_UPDATE_ROUTE", state.ServiceId("a"))
}

func TestNet1A(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being A:
	//          B
	//       1 /|
	//    1   / |
	// S --- A  |1
	//        \ |
	//       1 \|
	//          C

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("S", "B", "C"),
		Advertised: []state.ServiceId{"A"},
	}

	sr := AddLink(rs, NewMockEndpoint("S", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 1))

	// S's advertised routes
	h.NeighUpdate(rs, "S", "S", 0, 0)
	h.NeighUpdate(rs, "S", "A", 0, 1)
	h.NeighUpdate(rs, "S", "B", 0, 2)
	h.NeighUpdate(rs, "S", "C", 0, 2)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdate(rs, "B", "A", 0, 1)
	h.NeighUpdate(rs, "B", "C", 0, 1)
	h.NeighUpdate(rs, "B", "S", 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "C", 0, 0)
	h.NeighUpdate(rs, "C", "A", 0, 1)
	h.NeighUpdate(rs, "C", "B", 0, 1)
	h.NeighUpdate(rs, "C", "S", 0, 2)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	assert.Equal(t,
		`BROADCAST_UPDATE_ROUTE A (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE B (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE C (router: C, svc: C, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE S (router: S, svc: S, seqno: 0, metric: 1)`,
		a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: C, router: C, svc: C, seqno: 0, metric: 1)
S via (nh: S, router: S, svc: S, seqno: 0, metric: 1)`, rs.StringRoutes())

	// Suppose now the cost to S is increased to 10
	//          B
	//       1 /|
	//    10  / |
	// S --- A  |1
	//        \ |
	//       1 \|
	//          C
	sr.metric = 10
	ComputeRoutes(rs, h)
	// B advertises S to A
	h.NeighUpdate(rs, "B", "S", 0, 2)
	a = h.GetActions()
	assert.Equal(t,
		`REQUEST_SEQNO B (router: S, svc: S) 1 64`,
		a.String())

	// Suppose now the link to S goes down
	//          B
	//       1 /|
	//        / |
	// S     A  |1
	//        \ |
	//       1 \|
	//          C
	RemoveLink(rs, sr)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// We should retract our route to S
	a.AssertContains(t, "BROADCAST_UPDATE_ROUTE", state.ServiceId("S"), state.PubRoute{
		Source: state.Source{
			NodeId:    "S",
			ServiceId: "S",
		},
		FD: state.FD{
			Seqno:  0,
			Metric: state.INF,
		},
	})
}
