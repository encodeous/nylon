//go:build router_test

package core

import (
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

var (
	maxTime = time.Unix(1<<63-62135596801, 999999999)
)

func TestRouterBasicComputeRoutes(t *testing.T) {
	h := &RouterHarness{}
	rs := state.RouterState{
		Id:         "a",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("b", "c", "d"),
		Advertised: map[state.ServiceId]time.Time{"a": maxTime},
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
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: a, svc: a, seqno: 0, metric: 0)`, out.String())
}

func TestRouterNet1A_BasicRetraction(t *testing.T) {
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
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
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
		`BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: S, svc: S, seqno: 0, metric: 1)`,
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
	a.AssertContains(t, "BROADCAST_UPDATE_ROUTE", state.PubRoute{
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

func TestRouterNet2S_SolveStarvation(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being S:
	//    A
	// 1 /|        D(A) = 1
	//  / |       FD(A) = 1
	// S  |1
	//  \ |        D(B) = 2
	// 2 \|       FD(B) = 2
	//    B

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "S",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("A", "B"),
		Advertised: map[state.ServiceId]time.Time{"S": maxTime},
	}

	AS := AddLink(rs, NewMockEndpoint("A", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 2))

	// A's advertised routes
	h.NeighUpdate(rs, "A", "S", 0, 1)
	h.NeighUpdate(rs, "A", "A", 0, 0)
	h.NeighUpdate(rs, "A", "B", 0, 1)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdate(rs, "B", "A", 0, 1)
	h.NeighUpdate(rs, "B", "S", 0, 2)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	assert.Equal(t,
		`BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 2)
BROADCAST_UPDATE_ROUTE (router: S, svc: S, seqno: 0, metric: 0)`,
		a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 1)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 2)
S via (nh: S, router: S, svc: S, seqno: 0, metric: 0)`, rs.StringRoutes())

	// check feasibility distances
	assert.Equal(t, state.FD{Seqno: 0, Metric: 1}, rs.Sources[state.Source{NodeId: "A", ServiceId: "A"}])
	assert.Equal(t, state.FD{Seqno: 0, Metric: 2}, rs.Sources[state.Source{NodeId: "B", ServiceId: "B"}])
	assert.Equal(t, state.FD{Seqno: 0, Metric: 0}, rs.Sources[state.Source{NodeId: "S", ServiceId: "S"}])

	// Suppose now that the link to A goes down
	//    A
	//    |
	//    |       FD(A) = 1
	// S  |1
	//  \ |        D(B) = 2
	// 2 \|       FD(B) = 2
	//    B

	RemoveLink(rs, AS)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// We should retract our route to A
	a.AssertContains(t, "BROADCAST_UPDATE_ROUTE", state.PubRoute{
		Source: state.Source{
			NodeId:    "A",
			ServiceId: "A",
		},
		FD: state.FD{
			Seqno:  0,
			Metric: state.INF,
		},
	})
	// B acknowledges the retraction
	HandleAckRetract(rs, h, "B", "A")
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// check that we are indeed starved
	a.AssertNotContains(t, "BROADCAST_UPDATE_ROUTE")
	SolveStarvation(rs, h)
	a = h.GetActions()
	a.AssertContains(t, "BROADCAST_REQUEST_SEQNO", state.Source{NodeId: "A", ServiceId: "A"}, uint16(1), uint8(64))

	// suppose now that A receives the seqno request, sends an update to B, and B sends it to S
	h.NeighUpdate(rs, "B", "A", 1, 1)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	pr := state.PubRoute{
		Source: state.Source{
			NodeId:    "A",
			ServiceId: "A",
		},
		FD: state.FD{
			Seqno:  1,
			Metric: 3,
		},
	}
	a.AssertContains(t, "BROADCAST_UPDATE_ROUTE", pr)
	assert.Equal(t, pr, rs.Routes[("A")].PubRoute)
}

func TestRouterNet3A_HandleRetraction(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being A:
	//       2
	//    B ---- D
	// 1 /|     /
	//  / |    /
	// A  |1  / 1
	//  \ |  /
	// 3 \| /
	//    C

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
	}

	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 3))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", 0, 1)
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdate(rs, "B", "C", 0, 1)
	h.NeighUpdate(rs, "B", "D", 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "A", 0, 3)
	h.NeighUpdate(rs, "C", "B", 0, 1)
	h.NeighUpdate(rs, "C", "C", 0, 0)
	h.NeighUpdate(rs, "C", "D", 0, 1)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	assert.Equal(t,
		`BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 2)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 0, metric: 3)`,
		a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: B, router: C, svc: C, seqno: 0, metric: 2)
D via (nh: B, router: D, svc: D, seqno: 0, metric: 3)`, rs.StringRoutes())

	// Suppose now that the link between B and C goes down
	//       2
	//    B ---- D
	// 1 /      /
	//  /      /
	// A      / 1
	//  \    /
	// 3 \  /
	//    C

	// C will retract its route to B
	h.NeighUpdate(rs, "C", "B", 0, state.INF)
	a = h.GetActions()
	a.AssertContains(t, "ACK_RETRACT", state.NodeId("C"), state.ServiceId("B"))

	// B will retract its route to C and D
	h.NeighUpdate(rs, "B", "C", 0, state.INF)
	h.NeighUpdate(rs, "B", "D", 0, state.INF)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertContains(t, "ACK_RETRACT", state.NodeId("B"), state.ServiceId("C"))
	a.AssertContains(t, "ACK_RETRACT", state.NodeId("B"), state.ServiceId("D"))

	// D via C is feasible as C advertises D with a cost of 1, which is less than B's 2
	assert.Equal(t, uint16(4), rs.Routes["D"].Metric)
}

func TestRouterNet4A_OverlappingServiceHoldLoop(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being A:
	// Note, X is a service that both S and D advertise

	//            C
	//            | 1
	//  (S,X) --- A --- B --- (D,X)
	//         1     1     1

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("S", "B", "C"),
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
	}

	SA := AddLink(rs, NewMockEndpoint("S", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))

	// S's advertised routes
	h.NeighUpdate(rs, "S", "S", 0, 0)
	h.NeighUpdateSvc(rs, "S", "S", "X", 0, 0)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdateSvc(rs, "B", "D", "X", 0, 1)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "C", 0, 0)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: S, svc: S, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: S, svc: X, seqno: 0, metric: 1)`, a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: C, router: C, svc: C, seqno: 0, metric: 1)
S via (nh: S, router: S, svc: S, seqno: 0, metric: 1)
X via (nh: S, router: S, svc: X, seqno: 0, metric: 1)`, rs.StringRoutes())

	// Now, lets cut off both S from A and D from B, to see if we can produce a routing loop
	//            C
	//            | 1
	//  (S,X)     A --- B     (D,X)
	//               1
	RemoveLink(rs, SA)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: S, svc: S, seqno: 0, metric: 65535)
BROADCAST_UPDATE_ROUTE (router: S, svc: X, seqno: 0, metric: 65535)`, a.String())
	HandleAckRetract(rs, h, "B", "S")
	HandleAckRetract(rs, h, "B", "X")
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Empty(t, a, "Expect S and X to be held until C also sends ACK")
	HandleAckRetract(rs, h, "C", "S")
	HandleAckRetract(rs, h, "C", "X")
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: D, svc: X, seqno: 0, metric: 2)`, a.String())
	// B retracts D's published routes
	h.NeighUpdate(rs, "B", "D", 0, state.INF)
	h.NeighUpdateSvc(rs, "B", "D", "X", 0, state.INF)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `ACK_RETRACT B D
ACK_RETRACT B X
BROADCAST_UPDATE_ROUTE (router: D, svc: X, seqno: 0, metric: 65535)`, a.String())
}

func TestRouterNet4A_OverlappingServiceMetricIncrease(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being A:
	// Note, X is a service that both S and D advertise

	//            C
	//            | 1
	//  (S,X) --- A --- B --- (D,X)
	//         1     1     4

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("S", "B", "C"),
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
	}

	SA := AddLink(rs, NewMockEndpoint("S", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))

	// S's advertised routes
	h.NeighUpdate(rs, "S", "S", 0, 0)
	h.NeighUpdateSvc(rs, "S", "S", "X", 0, 0)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdateSvc(rs, "B", "D", "X", 0, 4)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "C", 0, 0)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: S, svc: S, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: S, svc: X, seqno: 0, metric: 1)`, a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: C, router: C, svc: C, seqno: 0, metric: 1)
S via (nh: S, router: S, svc: S, seqno: 0, metric: 1)
X via (nh: S, router: S, svc: X, seqno: 0, metric: 1)`, rs.StringRoutes())
	// Suppose now that SA's link cost increases to 2
	//            C
	//            | 1
	//  (S,X) --- A --- B --- (D,X)
	//         3     1     4
	SA.metric = 3
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Empty(t, a, "We should not change routes as S is still feasible")
	// However, for C, Cost(A, S) = 3 > 2, meaning S is no longer feasible via A
	// C should send a seqno request to A
	HandleSeqnoRequest(rs, h, "C", state.Source{NodeId: "S", ServiceId: "X"}, 1, 64)
	a = h.GetActions()
	// A should forward the request to S, decrementing the TTL by 1
	assert.Equal(t, `REQUEST_SEQNO S (router: S, svc: X) 1 63`, a.String())

	// Now, S replies with an update with a higher seqno
	h.NeighUpdateSvc(rs, "S", "S", "X", 1, 0)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: S, svc: X, seqno: 1, metric: 3)`, a.String())

	// Suppose, some other node also requests the seqno for S,X
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "S", ServiceId: "X"}, 1, 64)
	// A should not forward the request as we already have a route to S with an equivalent or higher seqno
	a = h.GetActions()
	// Instead, A should just reply with its current route to S,X
	assert.Equal(t, `UPDATE_ROUTE B (router: S, svc: X, seqno: 1, metric: 3)`, a.String())

	// Now, suppose some node requests the seqno for A

	// Req 1: A should not increase its seqno
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "A", ServiceId: "A"}, 0, 64)
	a = h.GetActions()
	assert.Equal(t, `UPDATE_ROUTE B (router: A, svc: A, seqno: 0, metric: 0)`, a.String())

	// Req 2: A should increase its seqno by 1
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "A", ServiceId: "A"}, 1, 64)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 1, metric: 0)`, a.String())

	// Req 3: A should increase its seqno to 5
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "A", ServiceId: "A"}, 5, 64)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 5, metric: 0)`, a.String())
}

func TestRouterNet5A_SelectedUnfeasibleUpdate(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being A:
	//       2
	//    B ---- D
	// 1 /|     /
	//  / |    /
	// A  |1  / 1
	//  \ |  /
	// 5 \| /
	//    C

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
	}

	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 5))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", 0, 1)
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdate(rs, "B", "C", 0, 1)
	h.NeighUpdate(rs, "B", "D", 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "A", 0, 5)
	h.NeighUpdate(rs, "C", "B", 0, 1)
	h.NeighUpdate(rs, "C", "C", 0, 0)
	h.NeighUpdate(rs, "C", "D", 0, 1)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	assert.Equal(t,
		`BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 2)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 0, metric: 3)`,
		a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: B, router: C, svc: C, seqno: 0, metric: 2)
D via (nh: B, router: D, svc: D, seqno: 0, metric: 3)`, rs.StringRoutes())

	// Suppose now that the link between B and C increases in metric
	//       2
	//    B ---- D
	// 1 /|     /
	//  / |    /
	// A  |3  / 1
	//  \ |  /
	// 5 \| /
	//    C

	h.NeighUpdate(rs, "B", "C", 0, 3)
	h.NeighUpdate(rs, "B", "D", 0, 3)
	h.NeighUpdate(rs, "C", "B", 0, 3)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `REQUEST_SEQNO B (router: C, svc: C) 1 64
REQUEST_SEQNO B (router: D, svc: D) 1 64`, a.String())

	// Now, we get the seqno updates from B
	h.NeighUpdate(rs, "B", "C", 1, 3)
	h.NeighUpdate(rs, "B", "D", 1, 3)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 1, metric: 4)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 1, metric: 4)`, a.String())
}

func TestRouter5A_GCRoutes(t *testing.T) {
	ConfigureConstants()
	state.RouteExpiryTime = -1 // for testing, we want routes to expire immediately
	// This test is for the following network with our router being A:
	//       3
	//    B ---- D
	// 1 /|     /
	//  / |    /
	// A  |1  / 1
	//  \ |  /
	// 5 \| /
	//    C

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
	}

	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 5))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", 0, 1)
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdate(rs, "B", "C", 0, 1)
	h.NeighUpdate(rs, "B", "D", 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "A", 0, 5)
	h.NeighUpdate(rs, "C", "B", 0, 1)
	h.NeighUpdate(rs, "C", "C", 0, 0)
	h.NeighUpdate(rs, "C", "D", 0, 1)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	assert.Equal(t,
		`BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 2)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 0, metric: 3)`,
		a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: B, router: C, svc: C, seqno: 0, metric: 2)
D via (nh: B, router: D, svc: D, seqno: 0, metric: 3)`, rs.StringRoutes())

	RunGC(rs, h) // expired routes are not held, so we do not need to wait for retraction ACK
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 65535)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 65535)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 0, metric: 65535)`, a.String())

	RunGC(rs, h)
	for _, neigh := range rs.Neighbours {
		assert.Empty(t, neigh.Routes, "Expected all routes to be expired")
	}
}

func TestRouterNet6A_ConvergeOptimal(t *testing.T) {
	ConfigureConstants()
	// This test is for the following network with our router being A:
	//       3
	//    B ---- D
	// 1 /      /
	//  /      /
	// A      / 1
	//       /
	//      /
	//    C

	h := &RouterHarness{}
	rs := &state.RouterState{
		Id:         "A",
		Seqno:      0,
		Routes:     make(map[state.ServiceId]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[state.ServiceId]time.Time{"A": maxTime},
	}

	AB := AddLink(rs, NewMockEndpoint("B", 1))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", 0, 1)
	h.NeighUpdate(rs, "B", "B", 0, 0)
	h.NeighUpdate(rs, "B", "C", 0, 4)
	h.NeighUpdate(rs, "B", "D", 0, 3)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	assert.Equal(t,
		`BROADCAST_UPDATE_ROUTE (router: A, svc: A, seqno: 0, metric: 0)
BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 1)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 5)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 0, metric: 4)`,
		a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: B, router: C, svc: C, seqno: 0, metric: 5)
D via (nh: B, router: D, svc: D, seqno: 0, metric: 4)`, rs.StringRoutes())

	// Suppose now, we introduce a new link
	//       3
	//    B ---- D
	// 1 /      /
	//  /      /
	// A      / 1
	//  \    /
	// 6 \  /
	//    C

	AC := AddLink(rs, NewMockEndpoint("C", 6))
	// C's advertised routes
	h.NeighUpdate(rs, "C", "B", 0, 4)
	h.NeighUpdate(rs, "C", "C", 0, 0)
	h.NeighUpdate(rs, "C", "D", 0, 1)

	// this should not change anything, as this route is not optimal
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// check that we converge to the correct table
	assert.Empty(t, a, "No changes expected")
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: B, router: C, svc: C, seqno: 0, metric: 5)
D via (nh: B, router: D, svc: D, seqno: 0, metric: 4)`, rs.StringRoutes())

	// Now, we improve the cost of AC to 2
	//       3
	//    B ---- D
	// 1 /      /
	//  /      /
	// A      / 1
	//  \    /
	// 2 \  /
	//    C
	AC.metric = 2
	// Now, C and B are closer via C instead of B
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// check that we converge to the correct table
	assert.Equal(t, ``, a.String()) // not a significant change, so we should not broadcast
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 1)
C via (nh: C, router: C, svc: C, seqno: 0, metric: 2)
D via (nh: C, router: D, svc: D, seqno: 0, metric: 3)`, rs.StringRoutes())

	// Now, AC degrades to 10000, and AB degrades to 12000
	AC.metric = 10000
	AB.metric = 12000
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Equal(t, `BROADCAST_UPDATE_ROUTE (router: B, svc: B, seqno: 0, metric: 12000)
BROADCAST_UPDATE_ROUTE (router: C, svc: C, seqno: 0, metric: 10000)
BROADCAST_UPDATE_ROUTE (router: D, svc: D, seqno: 0, metric: 10001)`, a.String())
	assert.Equal(t, `A via (nh: A, router: A, svc: A, seqno: 0, metric: 0)
B via (nh: B, router: B, svc: B, seqno: 0, metric: 12000)
C via (nh: C, router: C, svc: C, seqno: 0, metric: 10000)
D via (nh: C, router: D, svc: D, seqno: 0, metric: 10001)`, rs.StringRoutes())
}
