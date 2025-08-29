package core

import (
	"time"

	"github.com/encodeous/nylon/state"
)

func (n *Nylon) initPassiveClient(s *state.State) error {
	s.Env.RepeatTask(scanPassivePeers, state.ProbeDelay)
	return nil
}

func scanPassivePeers(s *state.State) error {
	n := Get[*Nylon](s)
	r := Get[*NylonRouter](s)
	for _, peer := range n.Device.GetPeers() {
		nid := s.FindNodeBy(state.NyPublicKey(peer.GetPublicKey()))

		if nid != nil {
			// check if we are the only node that is advertising this passive client, if so, we can apply the following optimization
			// As we are the only node advertising the client, we can permanently hold the route, and not expire it
			// This enables our passive client to be reachable even if it does not send any traffic for a long time (e.g. mobile device going to sleep)
			// If this device switches to another nylon node, that node will start advertising the client, and we will stop holding the route

			hasOtherAdvertisers := false
			for _, neigh := range s.Neighbours {
				for _, route := range neigh.Routes {
					if route.ServiceId == state.ServiceId(*nid) && route.NodeId != s.Id && route.FD.Metric != state.INF {
						hasOtherAdvertisers = true
						break
					}
				}
			}

			// TODO: we could make this expire after a longer period of time, like 24h. However, this would require our passive client to wait for the full route propagation time after 24 hours. (Might cause unexpected interruptions)

			if s.IsClient(*nid) && time.Now().Sub(peer.LastReceivedPacket()) < state.ClientDeadThreshold || !hasOtherAdvertisers {
				// we have a passive client
				ncfg := s.GetNode(*nid)

				for _, newSvc := range ncfg.Services {
					r.updatePassiveClient(s, newSvc, *nid)
				}
			}
		}
	}
	return nil
}
