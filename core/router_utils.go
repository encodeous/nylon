package core

import (
	"fmt"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
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

func newMetricReplacementPolicy(existing *protocol.Ny_Update, new *protocol.Ny_Update) {
	existing.SeqnoPush = existing.SeqnoPush || new.SeqnoPush
	existing.Metric = new.Metric
	if SeqnoLt(uint16(existing.Source.Seqno), uint16(new.Source.Seqno)) {
		existing.Source = new.Source
	}
}

func maxMetricReplacementPolicy(existing *protocol.Ny_Update, new *protocol.Ny_Update) {
	existing.SeqnoPush = existing.SeqnoPush || new.SeqnoPush
	existing.Metric = max(new.Metric, existing.Metric)
	if SeqnoLt(uint16(existing.Source.Seqno), uint16(new.Source.Seqno)) {
		existing.Source = new.Source
	}
}

func broadcastUpdate(update *protocol.Ny_Update, neighs []*state.Neighbour, resolve func(existing *protocol.Ny_Update, new *protocol.Ny_Update)) {
	//for _, neigh := range neighs {
	//	cur := neigh.IO.Updates[state.NodeId(update.Source.Id)]
	//	if cur != nil {
	//		resolve(cur, update)
	//	} else {
	//		cur = update
	//	}
	//	neigh.IO.Updates[state.NodeId(update.Source.Id)] = cur
	//}
}

func broadcastSeqnoRequest(src state.Source, neighs []*state.Neighbour) {
	//for _, neigh := range neighs {
	//	neigh.IO.SeqnoReq[src] = struct{}{}
	//}
}

func mapToPktSource(source *state.Source) *protocol.Ny_Source {
	panic("not implemented")
	//return &protocol.Ny_Source{
	//	Id:    string(source.Id),
	//	Seqno: uint32(source.Seqno),
	//}
}
func mapFromPktSource(source *protocol.Ny_Source) state.Source {
	panic("not implemented")
	//return state.Source{
	//	Id:    state.NodeId(source.Id),
	//	Seqno: uint16(source.Seqno),
	//}
}

func dbgPrintRouteTable(s *state.State) {
	r := Get[*NylonRouter](s)
	if state.DBG_log_route_table {
		buf := strings.Builder{}
		if len(r.Routes) != 0 {
			buf.WriteString("--- route table ---\n")
		}
		for _, route := range r.Routes {
			buf.WriteString(fmt.Sprintf("%s(%d) -> %s m=%d, fd=%d\n", route.NodeId, route.Seqno, route.Nh, route.Metric, route.FD))
		}
		if buf.Len() > 0 {
			s.Log.Debug(buf.String())
		}
	}
}

func dbgPrintRouteChanges(s *state.State, curRoute *state.SelRoute, newRoute *state.PubRoute, via state.NodeId, metric uint16) {
	//if state.DBG_log_route_changes {
	//	if curRoute == nil {
	//		s.Log.Debug(fmt.Sprintf("[rc] %s(%d) new [%d]%s", newRoute.Src.Id, newRoute.Src.Seqno, metric, via))
	//	} else if metric == state.INF || newRoute == nil {
	//		s.Log.Debug(fmt.Sprintf("[rc] %s ret %s(%d)", via, curRoute.Src.Id, curRoute.Src.Seqno))
	//	} else {
	//		s.Log.Debug(fmt.Sprintf("[rc] %s(%d) via [%d]%s / [%d]%s", curRoute.Src.Id, curRoute.Src.Seqno, metric, via, curRoute.PubMetric, curRoute.Nh))
	//	}
	//}
}

func NeighContainsFunc(s *state.RouterState, f func(neigh state.NodeId, route state.NeighRoute) bool) bool {
	for _, n := range s.Neighbours {
		for _, r := range n.Routes {
			if f(n.Id, r) {
				return true
			}
		}
	}
	return false
}
