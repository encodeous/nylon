package core

import (
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/state"
	"slices"
	"time"
)

func (n *Nylon) initPassiveClient(s *state.State) error {
	s.Env.RepeatTask(scanPassivePeers, state.ClientKeepaliveInterval)
	return nil
}

func scanPassivePeers(s *state.State) error {
	n := Get[*Nylon](s)
	r := Get[*Router](s)
	for _, peer := range n.Device.GetPeers() {
		nid := s.FindNodeBy(state.NyPublicKey(peer.GetPublicKey()))
		if nid != nil && s.IsClient(*nid) && time.Now().Sub(peer.LastReceivedPacket()) < state.ClientDeadThreshold {
			// we have a passive client
			if !slices.Contains(r.Clients, *nid) {
				r.Clients = append(r.Clients, *nid)
			}
			r.updatePassiveClient(*nid)
		}
	}
	return nil
}

func cleanPassivePeers(s *state.State) error {
	n := Get[*Nylon](s)
	r := Get[*Router](s)
	x := 0
	for _, client := range r.Clients {
		cCfg := s.GetClient(client)
		peer := n.Device.LookupPeer(device.NoisePublicKey(cCfg.PubKey))
		peer.CleanEndpoints()
		if peer == nil || time.Now().Sub(peer.LastReceivedPacket()) > state.ClientDeadThreshold {
			// dead client
			s.Log.Debug("passive client dead", "node", client)
		} else {
			r.Clients[x] = client
			x++
		}
	}
	r.Clients = r.Clients[:x]
	err := updateRoutes(s, false)
	if err != nil {
		return err
	}
	return nil
}
