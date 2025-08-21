package core

import (
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"slices"
)

func pushRouteTable(s *state.State, to *state.NodeId, sources *[]state.NodeId, seqnoPush bool) error {
	r := Get[*NylonRouter](s)
	dbgPrintRouteTable(s)

	neighs := s.Neighbours
	if to != nil {
		neighs = []*state.Neighbour{
			s.GetNeighbour(*to),
		}
	}

	// broadcast routes
	updates := make([]*protocol.Ny_Update, 0)

	// make self update
	updates = append(updates, &protocol.Ny_Update{
		Source: mapToPktSource(r.Self),
		Metric: 0,
	})
	broadcastUpdate(
		&protocol.Ny_Update{
			Source:    mapToPktSource(r.Self),
			Metric:    0,
			SeqnoPush: seqnoPush,
		},
		neighs,
		newMetricReplacementPolicy)

	// write route table
	if !s.DisableRouting {
		for _, route := range r.Routes {
			if sources != nil {
				if !slices.Contains(*sources, route.NodeId) {
					continue
				}
			}
			broadcastUpdate(
				&protocol.Ny_Update{
					Source:    mapToPktSource(&route.Source),
					Metric:    uint32(route.Metric),
					SeqnoPush: seqnoPush,
				},
				s.Neighbours,
				maxMetricReplacementPolicy)
		}
	}

	return nil
}

func flushIO(s *state.State) error {
	//n := Get[*Nylon](s)
	//for _, neigh := range s.Neighbours {
	//	// TODO, investigate effect of packet loss on control messages
	//	best := neigh.BestEndpoint()
	//	if best != nil && best.IsActive() {
	//		peer := n.Device.LookupPeer(device.NoisePublicKey(n.env.GetNode(best.Node()).PubKey))
	//		for {
	//			bundle := &protocol.TransportBundle{}
	//			tLength := 0
	//			flushedSeq := make([]state.Source, 0)
	//			flushedUpdate := make([]state.NodeId, 0)
	//
	//			// we can coalesce messages, but we need to make sure we don't fragment our UDP packet
	//
	//			for seqR, _ := range neigh.IO.SeqnoReq {
	//				req := &protocol.Ny{Type: &protocol.Ny_SeqnoRequestOp{
	//					SeqnoRequestOp: mapToPktSource(&seqR),
	//				}}
	//				if tLength+proto.Size(req) >= state.SafeMTU {
	//					goto send
	//				}
	//				flushedSeq = append(flushedSeq, seqR)
	//				bundle.Packets = append(bundle.Packets, req)
	//				tLength += proto.Size(req)
	//			}
	//
	//			for id, update := range neigh.IO.Updates {
	//				req := &protocol.Ny{Type: &protocol.Ny_RouteOp{
	//					RouteOp: update,
	//				}}
	//				if tLength+proto.Size(req) >= state.SafeMTU {
	//					goto send
	//				}
	//				flushedUpdate = append(flushedUpdate, id)
	//				bundle.Packets = append(bundle.Packets, req)
	//				tLength += proto.Size(req)
	//			}
	//
	//			if tLength == 0 {
	//				break
	//			}
	//		send:
	//			err := n.SendNylonBundle(bundle, nil, peer)
	//			if err != nil {
	//				return err
	//			}
	//			for _, update := range flushedUpdate {
	//				delete(neigh.IO.Updates, update)
	//			}
	//			for _, id := range flushedSeq {
	//				delete(neigh.IO.SeqnoReq, id)
	//			}
	//		}
	//	}
	//}
	return nil
}
