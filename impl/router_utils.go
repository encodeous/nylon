package impl

import (
	"fmt"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
)

func IncrementSeqno(s *state.State) {
	r := Get[*Router](s)
	r.Self.Seqno++
	// TODO: Signature
}

func broadcast(s *state.State, message proto.Message) {
	r := s.Neighbours
	n := Get[*Nylon](s)
	go func() {
		for _, neigh := range r {
			// TODO, investigate effect of packet loss on control messages
			best := neigh.BestEndpoint()
			if best != nil && best.IsActive() {
				marshal, err := proto.Marshal(message)
				if err != nil {
					s.Env.Log.Error("error while broadcasting", "err", err.Error())
				}
				n.Send(marshal, best)
			}
		}
	}()
}

func broadcastUpdates(s *state.State, updates []*protocol.Ny_Update, push bool) {
	pkt := protocol.Ny_UpdateBundle{
		SeqnoPush: push,
		Updates:   updates,
	}
	broadcast(s, &protocol.Ny{Type: &protocol.Ny_RouteOp{RouteOp: &pkt}})
}

func broadcastSeqnoRequest(s *state.State, src *protocol.Ny_Source) {
	broadcast(s, &protocol.Ny{Type: &protocol.Ny_SeqnoRequestOp{
		SeqnoRequestOp: src,
	}})
}

func mapToPktSource(source *state.Source) *protocol.Ny_Source {
	return &protocol.Ny_Source{
		Id:    string(source.Id),
		Seqno: uint32(source.Seqno),
		Sig:   source.Sig,
	}
}
func mapFromPktSource(source *protocol.Ny_Source) state.Source {
	return state.Source{
		Id:    state.Node(source.Id),
		Seqno: uint16(source.Seqno),
		Sig:   source.Sig,
	}
}

func dbgPrintRouteTable(s *state.State) {
	r := Get[*Router](s)
	if state.DBG_log_route_table {
		if len(r.Routes) != 0 {
			s.Log.Debug("--- route table ---")
		}
		for _, route := range r.Routes {
			s.Log.Debug(fmt.Sprintf("%s(%d) -> %s", route.Src.Id, route.Src.Seqno, route.Nh), "met", route.PubMetric, "fd", route.Fd, "ret", route.Retracted)
		}
	}

	if state.OtelEnabled {
		for _, route := range r.Routes {
			otelLog.Info("nylon.route.selected", "router", string(s.Id), "src", string(route.Src.Id), "nh", string(route.Nh), "ret", route.Retracted, "fd", route.Fd, "met", route.PubMetric, "seqno", route.Src.Seqno)
		}
	}
}

func dbgPrintRouteChanges(s *state.State, curRoute *state.Route, newRoute *state.PubRoute, via state.Node, metric uint16) {
	if state.DBG_log_route_changes {
		if curRoute == nil {
			s.Log.Debug(fmt.Sprintf("[rc] %s(%d) new [%d]%s", newRoute.Src.Id, newRoute.Src.Seqno, metric, via))
		} else if metric == state.INF || newRoute == nil {
			s.Log.Debug(fmt.Sprintf("[rc] %s ret %s(%d)", via, curRoute.Src.Id, curRoute.Src.Seqno))
		} else {
			s.Log.Debug(fmt.Sprintf("[rc] %s(%d) via [%d]%s / [%d]%s", curRoute.Src.Id, curRoute.Src.Seqno, metric, via, curRoute.PubMetric, curRoute.Nh))
		}
	}
}
