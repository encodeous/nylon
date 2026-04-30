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
	"github.com/stretchr/testify/assert"
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

type RouterHarness struct {
	actions []RouterEvent
}

type RouterEvent struct {
	Type string
	Args []any
}

const (
	eventSendAckRetract        = "send_ack_retract"
	eventSendRouteUpdate       = "send_route_update"
	eventBroadcastRouteUpdate  = "broadcast_route_update"
	eventSendSeqnoRequest      = "send_seqno_request"
	eventBroadcastSeqnoRequest = "broadcast_seqno_request"
	eventRouterLog             = "router_log"
)

func NewRouterEvent(eventType string, args ...any) RouterEvent {
	return RouterEvent{Type: eventType, Args: args}
}

func AckRetract(neigh state.NodeId, prefix netip.Prefix) RouterEvent {
	return NewRouterEvent(eventSendAckRetract, neigh, prefix)
}

func UpdateRoute(neigh state.NodeId, route state.PubRoute) RouterEvent {
	return NewRouterEvent(eventSendRouteUpdate, neigh, route)
}

func BroadcastUpdateRoute(route state.PubRoute) RouterEvent {
	return NewRouterEvent(eventBroadcastRouteUpdate, route)
}

func RequestSeqno(neigh state.NodeId, src state.Source, seqno uint16, hopCnt uint8) RouterEvent {
	return NewRouterEvent(eventSendSeqnoRequest, neigh, src, seqno, hopCnt)
}

func BroadcastRequestSeqno(src state.Source, seqno uint16, hopCnt uint8) RouterEvent {
	return NewRouterEvent(eventBroadcastSeqnoRequest, src, seqno, hopCnt)
}

func RouterLog(event string, desc string, args ...any) RouterEvent {
	eventArgs := []any{event, desc}
	eventArgs = append(eventArgs, args...)
	return NewRouterEvent(eventRouterLog, eventArgs...)
}

func (h *RouterHarness) TableInsertRoute(prefix netip.Prefix, route state.SelRoute) {

}

func (h *RouterHarness) TableDeleteRoute(prefix netip.Prefix) {

}

func (h *RouterHarness) SendAckRetract(neigh state.NodeId, prefix netip.Prefix) {
	h.actions = append(h.actions, AckRetract(neigh, prefix))
}

func (h *RouterHarness) SendRouteUpdate(neigh state.NodeId, advRoute state.PubRoute) {
	h.actions = append(h.actions, UpdateRoute(neigh, advRoute))
}

func (h *RouterHarness) BroadcastSendRouteUpdate(advRoute state.PubRoute) {
	h.actions = append(h.actions, BroadcastUpdateRoute(advRoute))
}

func (h *RouterHarness) RequestSeqno(neigh state.NodeId, src state.Source, seqno uint16, hopCnt uint8) {
	h.actions = append(h.actions, RequestSeqno(neigh, src, seqno, hopCnt))
}

func (h *RouterHarness) BroadcastRequestSeqno(src state.Source, seqno uint16, hopCnt uint8) {
	h.actions = append(h.actions, BroadcastRequestSeqno(src, seqno, hopCnt))
}

func (h *RouterHarness) RouterEvent(event string, desc string, args ...any) {
	h.actions = append(h.actions, RouterLog(event, desc, args...))
}

type HarnessEvents []RouterEvent

func (h HarnessEvents) String() string {
	out := make([]string, 0)
	for _, action := range h {
		cur := action.Type
		for _, arg := range action.Args {
			cur += " " + fmt.Sprint(arg)
		}
		out = append(out, cur)
	}
	slices.Sort(out)
	return strings.Join(out, "\n")
}

func (h *RouterHarness) GetActions() HarnessEvents {
	x := make([]RouterEvent, 0)
	for _, action := range h.actions {
		if action.Type != eventRouterLog {
			x = append(x, action)
		}
	}

	h.actions = make([]RouterEvent, 0)
	return x
}

func (e HarnessEvents) contains(eventType string, args ...any) bool {
	for _, event := range e {
		if event.Type == eventType {
			eventArgs := event.Args
			if len(eventArgs) >= len(args) {
				match := true
				for i, arg := range args {
					if !cmp.Equal(eventArgs[i], arg, cmpopts.EquateComparable(netip.Prefix{})) {
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

func (e HarnessEvents) containsEvent(expected RouterEvent) bool {
	return e.contains(expected.Type, expected.Args...)
}

func eventMatchArgs(event any, args []any) (string, []any) {
	if expected, ok := event.(RouterEvent); ok {
		return expected.Type, expected.Args
	}
	return fmt.Sprint(event), args
}

func (e HarnessEvents) AssertContains(t *testing.T, event any, args ...any) {
	eventType, eventArgs := eventMatchArgs(event, args)
	if e.contains(eventType, eventArgs...) {
		return
	}
	t.Fatal("Expected event not found: ", eventType, " with args: ", eventArgs, " in ", e)
}

func (e HarnessEvents) AssertNotContains(t *testing.T, event any, args ...any) {
	eventType, eventArgs := eventMatchArgs(event, args)
	if e.contains(eventType, eventArgs...) {
		t.Fatal("Unexpected event found: ", eventType, " with args: ", eventArgs, " in ", e)
	}
}

func (e HarnessEvents) AssertEqual(t *testing.T, expected ...RouterEvent) {
	t.Helper()
	assert.Equal(t, HarnessEvents(expected).String(), e.String())
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
