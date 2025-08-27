package core

import (
	"time"

	"github.com/encodeous/nylon/state"
)

func (n *Nylon) initPassiveClient(s *state.State) error {
	s.Env.RepeatTask(scanPassivePeers, state.ClientKeepaliveInterval)
	return nil
}

func scanPassivePeers(s *state.State) error {
	n := Get[*Nylon](s)
	r := Get[*NylonRouter](s)
	passiveServices := make([]state.ServiceId, 0)
	for _, peer := range n.Device.GetPeers() {
		nid := s.FindNodeBy(state.NyPublicKey(peer.GetPublicKey()))
		if nid != nil && s.IsClient(*nid) && time.Now().Sub(peer.LastReceivedPacket()) < state.ClientDeadThreshold {
			// we have a passive client
			ncfg := s.GetNode(*nid)

			for _, newSvc := range ncfg.Services {
				passiveServices = append(passiveServices, newSvc)
				r.updatePassiveClient(s, newSvc)
			}
		}
	}
	r.PassiveServices = passiveServices
	return nil
}
