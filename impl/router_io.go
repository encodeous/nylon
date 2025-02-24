package impl

import (
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
	"time"
)

// packet handlers
func routerHandleRouteUpdate(s *state.State, node state.Node, pkt *protocol.Ny_UpdateBundle) error {
	neigh := s.GetNeighbour(node)
	hasRetractions := false
	for _, update := range pkt.Updates {
		cur, ok := neigh.Routes[state.Node(update.Source.Id)]
		if ok {
			hasRetractions = hasRetractions || !cur.Retracted && update.Metric == uint32(state.INF)
		}
		neigh.Routes[state.Node(update.Source.Id)] = state.PubRoute{
			Src:           mapFromPktSource(update.Source),
			PubMetric:     uint16(update.Metric),
			Retracted:     update.Metric == uint32(state.INF),
			LastPublished: time.Now(),
		}
	}
	if hasRetractions || pkt.SeqnoPush {
		return updateRoutes(s)
	}
	return nil
}

func routerHandleSeqnoRequest(s *state.State, node state.Node, pkt *protocol.Ny_Source) error {
	r := Get[*Router](s)
	src := mapFromPktSource(pkt)

	// TODO: Verify src sig

	var fSrc *state.Source

	froute, ok := r.Routes[src.Id]

	if ok && SeqnoGt(froute.Src.Seqno, src.Seqno) {
		fSrc = &froute.Src
	} else if s.Id == src.Id {
		if r.Self.Seqno <= src.Seqno {
			r.Self.Seqno = src.Seqno
			IncrementSeqno(s)
		}
		fSrc = r.Self
	}

	if fSrc != nil {
		// we have a better one cached, no need to forward
		return pushSeqnoUpdate(s, []state.Node{fSrc.Id})
	} else {
		prev := r.SeqnoDedup.Get(src.Id)
		if prev != nil && SeqnoGe(prev.Value().Seqno, src.Seqno) {
			return nil // we have already sent such a request before
		}
		r.SeqnoDedup.Set(src.Id, src, ttlcache.DefaultTTL)
		broadcastSeqnoRequest(s, pkt)
	}
	return nil
}
