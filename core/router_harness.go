//go:build router_test

package core

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func ConfigureConstants() {
	state.HopCost = 0
	state.RouteExpiryTime = 10 * time.Hour
}

type MockEndpoint struct {
	node   state.NodeId
	metric uint32
	active bool
	remote bool
}

func (m MockEndpoint) Node() state.NodeId {
	return m.node
}

func (m MockEndpoint) UpdatePing(ping time.Duration) {
	m.metric = min(uint32(ping.Microseconds()), state.INF)
}

func (m MockEndpoint) Metric() uint32 {
	return m.metric
}

func (m MockEndpoint) IsRemote() bool {
	return m.remote
}

func (m MockEndpoint) IsActive() bool {
	return m.active
}

func (m MockEndpoint) AsNylonEndpoint() *state.NylonEndpoint {
	return nil
}

func NewMockEndpoint(node state.NodeId, metric uint32) *MockEndpoint {
	return &MockEndpoint{
		node:   node,
		metric: metric,
		active: true,
		remote: false,
	}
}

type HarnessEvent struct {
	Message string
	Args    []any
}

func MakeEvent(msg string, args ...any) HarnessEvent {
	return HarnessEvent{
		Message: msg,
		Args:    args,
	}
}

type RouterHarness struct {
	actions []HarnessEvent
}

func (h *RouterHarness) TableInsertRoute(prefix netip.Prefix, route state.SelRoute) {

}

func (h *RouterHarness) TableDeleteRoute(prefix netip.Prefix) {

}

func (h *RouterHarness) SendAckRetract(neigh state.NodeId, prefix netip.Prefix) {
	h.actions = append(h.actions, MakeEvent("ACK_RETRACT", neigh, prefix))
}

func (h *RouterHarness) SendRouteUpdate(neigh state.NodeId, advRoute state.PubRoute) {
	h.actions = append(h.actions, MakeEvent("UPDATE_ROUTE", neigh, advRoute))
}

func (h *RouterHarness) BroadcastSendRouteUpdate(advRoute state.PubRoute) {
	h.actions = append(h.actions, MakeEvent("BROADCAST_UPDATE_ROUTE", advRoute))
}

func (h *RouterHarness) RequestSeqno(neigh state.NodeId, src state.Source, seqno uint16, hopCnt uint8) {
	h.actions = append(h.actions, MakeEvent("REQUEST_SEQNO", neigh, src, seqno, hopCnt))
}

func (h *RouterHarness) BroadcastRequestSeqno(src state.Source, seqno uint16, hopCnt uint8) {
	h.actions = append(h.actions, MakeEvent("BROADCAST_REQUEST_SEQNO", src, seqno, hopCnt))
}

func (h *RouterHarness) Log(event RouterEvent, desc string, args ...any) {
	x := make([]any, 0)
	x = append(x, event)
	x = append(x, desc)
	x = append(x, args...)
	h.actions = append(h.actions, MakeEvent("LOG", x...))
}

type HarnessEvents []HarnessEvent

func (h HarnessEvents) String() string {
	out := make([]string, 0)
	for _, action := range h {
		cur := action.Message
		for _, arg := range action.Args {
			cur += " " + fmt.Sprint(arg)
		}
		out = append(out, cur)
	}
	slices.Sort(out)
	return strings.Join(out, "\n")
}

func (h *RouterHarness) GetActions() HarnessEvents {
	x := make([]HarnessEvent, 0)
	for _, action := range h.actions {
		if action.Message != "LOG" {
			x = append(x, action)
		}
	}

	h.actions = make([]HarnessEvent, 0)
	return x
}

func (e HarnessEvents) contains(msg string, args ...any) bool {
	for _, event := range e {
		if event.Message == msg {
			if len(event.Args) >= len(args) {
				match := true
				for i, arg := range args {
					if !cmp.Equal(event.Args[i], arg, cmpopts.EquateComparable(netip.Prefix{})) {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		}
	}
	return false

}

func (e HarnessEvents) AssertContains(t *testing.T, msg string, args ...any) {
	if e.contains(msg, args...) {
		return
	}
	t.Fatal("Expected event not found: ", msg, " with args: ", args, " in ", e)
}

func (e HarnessEvents) AssertNotContains(t *testing.T, msg string, args ...any) {
	if e.contains(msg, args...) {
		t.Fatal("Unexpected event found: ", msg, " with args: ", args, " in ", e)
	}
}

func MakeNeighbours(ids ...state.NodeId) []*state.Neighbour {
	neighs := make([]*state.Neighbour, 0, len(ids))
	for _, id := range ids {
		neighs = append(neighs, &state.Neighbour{
			Id:     id,
			Routes: make(map[netip.Prefix]state.NeighRoute),
		})
	}
	return neighs
}

func MakePubRoute(nodeId state.NodeId, prefix netip.Prefix, seqno uint16, metric uint32) state.PubRoute {
	return state.PubRoute{
		Source: state.Source{
			NodeId: nodeId,
			Prefix: prefix,
		},
		FD: state.FD{
			Seqno:  seqno,
			Metric: metric,
		},
	}
}

func AddLink(r *state.RouterState, ep *MockEndpoint) *MockEndpoint {
	for _, n := range r.Neighbours {
		if n.Id == ep.Node() {
			n.Eps = append(n.Eps, ep)
			return ep
		}
	}
	return nil
}

func RemoveLink(r *state.RouterState, ep *MockEndpoint) {
	for _, n := range r.Neighbours {
		if n.Id == ep.Node() {
			for i, e := range n.Eps {
				if e == ep {
					n.Eps = append(n.Eps[:i], n.Eps[i+1:]...)
					return
				}
			}
		}
	}
}

func (h *RouterHarness) NeighUpdate(rs *state.RouterState, neighId state.NodeId, nodeId state.NodeId, prefix netip.Prefix, seqno uint16, metric uint32) {
	HandleNeighbourUpdate(rs, h, neighId, MakePubRoute(nodeId, prefix, seqno, metric))
}

func (h *RouterHarness) NeighUpdateSvc(rs *state.RouterState, neighId state.NodeId, nodeId state.NodeId, prefix netip.Prefix, seqno uint16, metric uint32) {
	HandleNeighbourUpdate(rs, h, neighId, MakePubRoute(nodeId, prefix, seqno, metric))
}
