//go:build router_test

package core

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

var (
	maxTime = time.Unix(1<<63-62135596801, 999999999)
)

// Helper function to convert test node IDs to prefixes
// Maps single letter IDs to IP addresses in 10.0.0.x/32 range
func nodeToPrefix(nodeId string) netip.Prefix {
	var ipByte byte
	if len(nodeId) > 0 {
		ipByte = strings.ToLower(nodeId)[0] - 'a' + 1
	}
	return netip.MustParsePrefix(fmt.Sprintf("10.0.0.%d/32", ipByte))
}

func TestRouterBasicComputeRoutes(t *testing.T) {
	h := &RouterHarness{}
	aPrefix := nodeToPrefix("a")
	rs := state.RouterState{
		Id:         "a",
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("b", "c", "d"),
		Advertised: map[netip.Prefix]state.Advertisement{aPrefix: {NodeId: state.NodeId("a"), Expiry: maxTime}},
	}
	ComputeRoutes(&rs, h)
	// we should have only routes to ourselves
	if len(rs.Routes) != 1 {
		t.Errorf("Expected 1 route, got %d", len(rs.Routes))
	}
	if _, ok := rs.Routes[aPrefix]; !ok {
		t.Errorf("Expected route to service 'a', but it was not found")
	}
	out := h.GetActions()
	out.AssertContains(t, BroadcastUpdateRoute(MakePubRoute("a", aPrefix, 0, 0)))
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
	aPrefix := nodeToPrefix("A")
	rs := &state.RouterState{
		Id:         "A",
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("S", "B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{aPrefix: {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	sr := AddLink(rs, NewMockEndpoint("S", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 1))

	// S's advertised routes
	h.NeighUpdate(rs, "S", "S", nodeToPrefix("S"), 0, 0)
	h.NeighUpdate(rs, "S", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "S", "B", nodeToPrefix("B"), 0, 2)
	h.NeighUpdate(rs, "S", "C", nodeToPrefix("C"), 0, 2)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, 1)
	h.NeighUpdate(rs, "B", "S", nodeToPrefix("S"), 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)
	h.NeighUpdate(rs, "C", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, 1)
	h.NeighUpdate(rs, "C", "S", nodeToPrefix("S"), 0, 2)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("S"), 0, 1)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.19/32 via (nh: S, router: S, prefix: 10.0.0.19/32, seqno: 0, metric: 1)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: C, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 1)`, rs.StringRoutes())

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
	h.NeighUpdate(rs, "B", "S", nodeToPrefix("S"), 0, 2)
	a = h.GetActions()
	a.AssertEqual(t, RequestSeqno("B", state.Source{NodeId: "S", Prefix: nodeToPrefix("S")}, 1, 64))

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
	a.AssertContains(t, BroadcastUpdateRoute(state.PubRoute{
		Source: state.Source{
			NodeId: "S",
			Prefix: nodeToPrefix("S"),
		},
		FD: state.FD{
			Seqno:  0,
			Metric: state.INF,
		},
	}))
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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("A", "B"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("S"): {NodeId: state.NodeId("S"), Expiry: maxTime}},
	}

	AS := AddLink(rs, NewMockEndpoint("A", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 2))

	// A's advertised routes
	h.NeighUpdate(rs, "A", "S", nodeToPrefix("S"), 0, 1)
	h.NeighUpdate(rs, "A", "A", nodeToPrefix("A"), 0, 0)
	h.NeighUpdate(rs, "A", "B", nodeToPrefix("B"), 0, 1)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "B", "S", nodeToPrefix("S"), 0, 2)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 2)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("S"), 0, 0)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 1)
10.0.0.19/32 via (nh: S, router: S, prefix: 10.0.0.19/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 2)`, rs.StringRoutes())

	// check feasibility distances
	assert.Equal(t, state.FD{Seqno: 0, Metric: 1}, rs.Sources[state.Source{NodeId: "A", Prefix: nodeToPrefix("A")}])
	assert.Equal(t, state.FD{Seqno: 0, Metric: 2}, rs.Sources[state.Source{NodeId: "B", Prefix: nodeToPrefix("B")}])
	assert.Equal(t, state.FD{Seqno: 0, Metric: 0}, rs.Sources[state.Source{NodeId: "S", Prefix: nodeToPrefix("S")}])

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
	a.AssertContains(t, BroadcastUpdateRoute(state.PubRoute{
		Source: state.Source{
			NodeId: "A",
			Prefix: nodeToPrefix("A"),
		},
		FD: state.FD{
			Seqno:  0,
			Metric: state.INF,
		},
	}))
	// B acknowledges the retraction
	HandleAckRetract(rs, h, "B", nodeToPrefix("A"))
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// check that we are indeed starved
	a.AssertNotContains(t, BroadcastUpdateRoute(state.PubRoute{}))
	SolveStarvation(rs, h)
	a = h.GetActions()
	a.AssertContains(t, BroadcastRequestSeqno(state.Source{NodeId: "A", Prefix: nodeToPrefix("A")}, uint16(1), uint8(64)))

	// suppose now that A receives the seqno request, sends an update to B, and B sends it to S
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 1, 1)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	pr := state.PubRoute{
		Source: state.Source{
			NodeId: "A",
			Prefix: nodeToPrefix("A"),
		},
		FD: state.FD{
			Seqno:  1,
			Metric: 3,
		},
	}
	a.AssertContains(t, BroadcastUpdateRoute(pr))
	assert.Equal(t, pr, rs.Routes[nodeToPrefix("A")].PubRoute)
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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 3))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, 1)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "A", nodeToPrefix("A"), 0, 3)
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, 1)
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)
	h.NeighUpdate(rs, "C", "D", nodeToPrefix("D"), 0, 1)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 2)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 0, 3)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: B, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 2)
10.0.0.4/32 via (nh: B, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 3)`, rs.StringRoutes())

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
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, state.INF)
	a = h.GetActions()
	a.AssertContains(t, AckRetract(state.NodeId("C"), nodeToPrefix("B")))

	// B will retract its route to C and D
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, state.INF)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, state.INF)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertContains(t, AckRetract(state.NodeId("B"), nodeToPrefix("C")))
	a.AssertContains(t, AckRetract(state.NodeId("B"), nodeToPrefix("D")))

	// D via C is feasible as C advertises D with a cost of 1, which is less than B's 2
	assert.Equal(t, uint32(4), rs.Routes[nodeToPrefix("D")].Metric)
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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("S", "B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	SA := AddLink(rs, NewMockEndpoint("S", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))

	// S's advertised routes
	h.NeighUpdate(rs, "S", "S", nodeToPrefix("S"), 0, 0)
	h.NeighUpdateSvc(rs, "S", "S", nodeToPrefix("X"), 0, 0)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdateSvc(rs, "B", "D", nodeToPrefix("X"), 0, 1)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("S"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("X"), 0, 1)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.19/32 via (nh: S, router: S, prefix: 10.0.0.19/32, seqno: 0, metric: 1)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.24/32 via (nh: S, router: S, prefix: 10.0.0.24/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: C, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 1)`, rs.StringRoutes())

	// Now, lets cut off both S from A and D from B, to see if we can produce a routing loop
	//            C
	//            | 1
	//  (S,X)     A --- B     (D,X)
	//               1
	RemoveLink(rs, SA)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("X"), 0, 2)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("S"), 0, state.INF)),
	)
	HandleAckRetract(rs, h, "B", nodeToPrefix("S"))
	HandleAckRetract(rs, h, "B", nodeToPrefix("X"))
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Empty(t, a, "Expect S to be held until C also sends ACK. X is now via D, so it is not held.")
	HandleAckRetract(rs, h, "C", nodeToPrefix("S"))
	HandleAckRetract(rs, h, "C", nodeToPrefix("X"))
	ComputeRoutes(rs, h)
	a = h.GetActions()
	assert.Empty(t, a, "S is now fully retracted. X is already active via D.")
	// B retracts D's published routes
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, state.INF)
	h.NeighUpdateSvc(rs, "B", "D", nodeToPrefix("X"), 0, state.INF)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertEqual(t,
		AckRetract("B", nodeToPrefix("X")),
		AckRetract("B", nodeToPrefix("D")),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("X"), 0, state.INF)),
	)
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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("S", "B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	SA := AddLink(rs, NewMockEndpoint("S", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))

	// S's advertised routes
	h.NeighUpdate(rs, "S", "S", nodeToPrefix("S"), 0, 0)
	h.NeighUpdateSvc(rs, "S", "S", nodeToPrefix("X"), 0, 0)

	// B's advertised routes
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdateSvc(rs, "B", "D", nodeToPrefix("X"), 0, 4)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("S"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("X"), 0, 1)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.19/32 via (nh: S, router: S, prefix: 10.0.0.19/32, seqno: 0, metric: 1)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.24/32 via (nh: S, router: S, prefix: 10.0.0.24/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: C, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 1)`, rs.StringRoutes())
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
	HandleSeqnoRequest(rs, h, "C", state.Source{NodeId: "S", Prefix: nodeToPrefix("X")}, 1, 64)
	a = h.GetActions()
	// A should forward the request to S, decrementing the TTL by 1
	a.AssertEqual(t, RequestSeqno("S", state.Source{NodeId: "S", Prefix: nodeToPrefix("X")}, 1, 63))

	// Now, S replies with an update with a higher seqno
	h.NeighUpdateSvc(rs, "S", "S", nodeToPrefix("X"), 1, 0)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertEqual(t, BroadcastUpdateRoute(MakePubRoute("S", nodeToPrefix("X"), 1, 3)))

	// Suppose, some other node also requests the seqno for S,X
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "S", Prefix: nodeToPrefix("X")}, 1, 64)
	// A should not forward the request as we already have a route to S with an equivalent or higher seqno
	a = h.GetActions()
	// Instead, A should just reply with its current route to S,X
	a.AssertEqual(t, UpdateRoute("B", MakePubRoute("S", nodeToPrefix("X"), 1, 3)))

	// Now, suppose some node requests the seqno for A

	// Req 1: A should not increase its seqno
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "A", Prefix: nodeToPrefix("A")}, 0, 64)
	a = h.GetActions()
	a.AssertEqual(t, UpdateRoute("B", MakePubRoute("A", nodeToPrefix("A"), 0, 0)))

	// Req 2: A should increase its seqno by 1
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "A", Prefix: nodeToPrefix("A")}, 1, 64)
	a = h.GetActions()
	a.AssertEqual(t, BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 1, 0)))

	// Req 3: A should increase its seqno to 5
	HandleSeqnoRequest(rs, h, "B", state.Source{NodeId: "A", Prefix: nodeToPrefix("A")}, 5, 64)
	a = h.GetActions()
	a.AssertEqual(t, BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 5, 0)))
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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 5))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, 1)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "A", nodeToPrefix("A"), 0, 5)
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, 1)
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)
	h.NeighUpdate(rs, "C", "D", nodeToPrefix("D"), 0, 1)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 2)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 0, 3)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: B, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 2)
10.0.0.4/32 via (nh: B, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 3)`, rs.StringRoutes())

	// Suppose now that the link between B and C increases in metric
	//       2
	//    B ---- D
	// 1 /|     /
	//  / |    /
	// A  |3  / 1
	//  \ |  /
	// 5 \| /
	//    C

	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, 3)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, 3)
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, 3)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertEqual(t,
		RequestSeqno("B", state.Source{NodeId: "C", Prefix: nodeToPrefix("C")}, 1, 64),
		RequestSeqno("B", state.Source{NodeId: "D", Prefix: nodeToPrefix("D")}, 1, 64),
	)

	// Now, we get the seqno updates from B
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 1, 3)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 1, 3)
	ComputeRoutes(rs, h)
	a = h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 1, 4)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 1, 4)),
	)
}

func TestRouter_BackupRouteOverridesHeldRoute(t *testing.T) {
	ConfigureConstants()
	// Topology:
	// A --(1)-- C
	// A --(1)-- B --(10)-- C
	// Initially A prefers A-C path.
	// When A-C fails, A should be starved, request a seqno, and then switch to A-B-C.

	h := &RouterHarness{}
	cPrefix := nodeToPrefix("C")
	rs := &state.RouterState{
		Id:         "A",
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	AC := AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))

	// C's advertisement via direct link
	h.NeighUpdate(rs, "C", "C", cPrefix, 0, 0)
	// B's advertisement of C (cost 10). This is initially UNFEASIBLE because 10 > 1 (current FD).
	h.NeighUpdate(rs, "B", "C", cPrefix, 0, 10)

	ComputeRoutes(rs, h)

	// Initially A prefers C via direct link
	assert.Equal(t, "C", string(rs.Routes[cPrefix].Nh))
	assert.Equal(t, uint32(1), rs.Routes[cPrefix].Metric)

	// Now AC link goes down
	RemoveLink(rs, AC)
	ComputeRoutes(rs, h)

	// A-C route should be retracted and held as INF
	assert.Equal(t, state.INF, rs.Routes[cPrefix].Metric)

	// A should realize it's starved and request a higher seqno
	SolveStarvation(rs, h)
	h.GetActions().AssertContains(t, BroadcastRequestSeqno(state.Source{NodeId: "C", Prefix: cPrefix}, uint16(1), uint8(64)))

	// Now B advertises C with the higher seqno (1). This is now FEASIBLE.
	h.NeighUpdate(rs, "B", "C", cPrefix, 1, 10)
	ComputeRoutes(rs, h)

	// A should now successfully switch to B
	assert.Equal(t, "B", string(rs.Routes[cPrefix].Nh))
	assert.Equal(t, uint32(11), rs.Routes[cPrefix].Metric)
}

func TestRouter_RetractedByClearedWhenHeldRouteRecovers(t *testing.T) {
	ConfigureConstants()
	// Topology:
	// A --(1)-- C
	// A --(1)-- B --(10)-- C
	// A --(1)-- D
	// D keeps the held route alive after B acknowledges the retraction.

	h := &RouterHarness{}
	cPrefix := nodeToPrefix("C")
	rs := &state.RouterState{
		Id:         "A",
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C", "D"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	AC := AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("D", 1))

	h.NeighUpdate(rs, "C", "C", cPrefix, 0, 0)
	h.NeighUpdate(rs, "B", "C", cPrefix, 0, 10)
	ComputeRoutes(rs, h)

	assert.Equal(t, "C", string(rs.Routes[cPrefix].Nh))
	assert.Equal(t, uint32(1), rs.Routes[cPrefix].Metric)

	RemoveLink(rs, AC)
	ComputeRoutes(rs, h)
	assert.Equal(t, state.INF, rs.Routes[cPrefix].Metric)

	HandleAckRetract(rs, h, "B", cPrefix)
	assert.Equal(t, []state.NodeId{"B"}, rs.Routes[cPrefix].RetractedBy)

	h.NeighUpdate(rs, "B", "C", cPrefix, 1, 10)
	ComputeRoutes(rs, h)

	assert.Equal(t, "B", string(rs.Routes[cPrefix].Nh))
	assert.Equal(t, uint32(11), rs.Routes[cPrefix].Metric)
	assert.Empty(t, rs.Routes[cPrefix].RetractedBy)
}

func TestRouter_AckRetractIgnoredForFiniteRoute(t *testing.T) {
	ConfigureConstants()

	h := &RouterHarness{}
	cPrefix := nodeToPrefix("C")
	rs := &state.RouterState{
		Id:         "A",
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	_ = AddLink(rs, NewMockEndpoint("C", 1))
	_ = AddLink(rs, NewMockEndpoint("B", 1))

	h.NeighUpdate(rs, "C", "C", cPrefix, 0, 0)
	ComputeRoutes(rs, h)

	assert.Equal(t, "C", string(rs.Routes[cPrefix].Nh))
	assert.Equal(t, uint32(1), rs.Routes[cPrefix].Metric)

	HandleAckRetract(rs, h, "B", cPrefix)
	assert.Empty(t, rs.Routes[cPrefix].RetractedBy)
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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	_ = AddLink(rs, NewMockEndpoint("B", 1))
	_ = AddLink(rs, NewMockEndpoint("C", 5))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, 1)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, 2)

	// C's advertised routes
	h.NeighUpdate(rs, "C", "A", nodeToPrefix("A"), 0, 5)
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, 1)
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)
	h.NeighUpdate(rs, "C", "D", nodeToPrefix("D"), 0, 1)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 2)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 0, 3)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: B, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 2)
10.0.0.4/32 via (nh: B, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 3)`, rs.StringRoutes())

	RunGC(rs, h) // expired routes are not held, so we do not need to wait for retraction ACK
	a = h.GetActions()
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, state.INF)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, state.INF)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 0, state.INF)),
	)

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
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: MakeNeighbours("B", "C"),
		Advertised: map[netip.Prefix]state.Advertisement{nodeToPrefix("A"): {NodeId: state.NodeId("A"), Expiry: maxTime}},
	}

	AB := AddLink(rs, NewMockEndpoint("B", 1))

	// B's advertised routes
	h.NeighUpdate(rs, "B", "A", nodeToPrefix("A"), 0, 1)
	h.NeighUpdate(rs, "B", "B", nodeToPrefix("B"), 0, 0)
	h.NeighUpdate(rs, "B", "C", nodeToPrefix("C"), 0, 4)
	h.NeighUpdate(rs, "B", "D", nodeToPrefix("D"), 0, 3)

	ComputeRoutes(rs, h)
	a := h.GetActions()
	// check that we converge to the correct table
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("A", nodeToPrefix("A"), 0, 0)),
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 1)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 5)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 0, 4)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: B, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 5)
10.0.0.4/32 via (nh: B, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 4)`, rs.StringRoutes())

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
	h.NeighUpdate(rs, "C", "B", nodeToPrefix("B"), 0, 4)
	h.NeighUpdate(rs, "C", "C", nodeToPrefix("C"), 0, 0)
	h.NeighUpdate(rs, "C", "D", nodeToPrefix("D"), 0, 1)

	// this should not change anything, as this route is not optimal
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// check that we converge to the correct table
	assert.Empty(t, a, "No changes expected")
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: B, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 5)
10.0.0.4/32 via (nh: B, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 4)`, rs.StringRoutes())

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
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 1)
10.0.0.3/32 via (nh: C, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 2)
10.0.0.4/32 via (nh: C, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 3)`, rs.StringRoutes())

	// Now, AC degrades to 10000000, and AB degrades to 12000000
	AC.metric = 10_000_000
	AB.metric = 12_000_000
	ComputeRoutes(rs, h)
	a = h.GetActions()
	// this is a significant change, so we should broadcast
	a.AssertEqual(t,
		BroadcastUpdateRoute(MakePubRoute("B", nodeToPrefix("B"), 0, 12_000_000)),
		BroadcastUpdateRoute(MakePubRoute("C", nodeToPrefix("C"), 0, 10_000_000)),
		BroadcastUpdateRoute(MakePubRoute("D", nodeToPrefix("D"), 0, 10_000_001)),
	)
	assert.Equal(t, `10.0.0.1/32 via (nh: A, router: A, prefix: 10.0.0.1/32, seqno: 0, metric: 0)
10.0.0.2/32 via (nh: B, router: B, prefix: 10.0.0.2/32, seqno: 0, metric: 12000000)
10.0.0.3/32 via (nh: C, router: C, prefix: 10.0.0.3/32, seqno: 0, metric: 10000000)
10.0.0.4/32 via (nh: C, router: D, prefix: 10.0.0.4/32, seqno: 0, metric: 10000001)`, rs.StringRoutes())
}
