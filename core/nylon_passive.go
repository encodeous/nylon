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
		if nid != nil && s.IsClient(*nid) && time.Now().Sub(peer.LastReceivedPacket()) < state.ClientDeadThreshold {
			// we have a passive client
			ncfg := s.GetNode(*nid)

			for _, newSvc := range ncfg.Services {
				r.updatePassiveClient(s, newSvc, *nid)
			}
		}
	}
	return nil
}
