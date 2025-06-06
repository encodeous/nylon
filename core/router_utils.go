package core

import (
	"fmt"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
	"net/netip"
	"strings"
)

func AddrToPrefix(addr netip.Addr) netip.Prefix {
	res, err := addr.Prefix(addr.BitLen())
	if err != nil {
		panic(err)
	}
	return res
}

func broadcast(s *state.State, message proto.Message, neighs []*state.Neighbour) {
	n := Get[*Nylon](s)
	go func() {
		for _, neigh := range neighs {
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

func broadcastUpdates(s *state.State, updates []*protocol.Ny_Update, push bool, neighs []*state.Neighbour) {
	pkt := protocol.Ny_UpdateBundle{
		SeqnoPush: push,
		Updates:   updates,
	}
	broadcast(s, &protocol.Ny{Type: &protocol.Ny_RouteOp{RouteOp: &pkt}}, neighs)
}

func broadcastSeqnoRequest(s *state.State, src *protocol.Ny_Source, neighs []*state.Neighbour) {
	broadcast(s, &protocol.Ny{Type: &protocol.Ny_SeqnoRequestOp{
		SeqnoRequestOp: src,
	}}, neighs)
}

func mapToPktSource(source *state.Source) *protocol.Ny_Source {
	return &protocol.Ny_Source{
		Id:    string(source.Id),
		Seqno: uint32(source.Seqno),
	}
}
func mapFromPktSource(source *protocol.Ny_Source) state.Source {
	return state.Source{
		Id:    state.NodeId(source.Id),
		Seqno: uint16(source.Seqno),
	}
}

func dbgPrintRouteTable(s *state.State) {
	r := Get[*Router](s)
	if state.DBG_log_route_table {
		buf := strings.Builder{}
		if len(r.Routes) != 0 {
			buf.WriteString("--- route table ---\n")
		}
		for _, route := range r.Routes {
			buf.WriteString(fmt.Sprintf("%s(%d) -> %s m=%d, fd=%d, ret=%v\n", route.Src.Id, route.Src.Seqno, route.Nh, route.PubMetric, route.Fd, route.Retracted))
		}
		if buf.Len() > 0 {
			s.Log.Debug(buf.String())
		}
	}
}

func dbgPrintRouteChanges(s *state.State, curRoute *state.Route, newRoute *state.PubRoute, via state.NodeId, metric uint16) {
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
