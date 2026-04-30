package core

import (
	"time"

	"github.com/encodeous/nylon/state"
)

func (n *Nylon) initPassiveClient() error {
	n.RepeatTask(func() error {
		return scanPassivePeers(n)
	}, state.ProbeDelay)
	return nil
}

func scanPassivePeers(n *Nylon) error {
	r := n.Router
	for _, peer := range n.Device.GetPeers() {
		nid := n.FindNodeBy(state.NyPublicKey(peer.GetPublicKey()))

		if nid != nil {
			// check if we are the only node that is advertising this passive client, if so, we can apply the following optimization
			// As we are the only node advertising the client, we can permanently hold the route (with a very high metric), and not expire it
			// This enables our passive client to be reachable even if it does not send any traffic for a long time (e.g. mobile device going to sleep)
			// If this device switches to another nylon node, that node will start advertising the client, and we will stop holding the route

			hasOtherAdvertisers := false
			ncfg := n.GetNode(*nid)
			for _, prefix := range ncfg.Prefixes {
				for _, neigh := range n.RouterState.Neighbours {
					for _, route := range neigh.Routes {
						if route.Prefix == prefix.GetPrefix() && route.NodeId != n.LocalCfg.Id && route.FD.Metric != state.INF {
							hasOtherAdvertisers = true
							goto foundAdvertiser
						}
					}
				}
			}
		foundAdvertiser:

			// TODO: we could make this expire after a longer period of time, like 24h. However, this would require our passive client to wait for the full route propagation time after 24 hours. (Might cause unexpected interruptions)

			recentlyUpdated := time.Since(peer.LastReceivedPacket()) < state.ClientDeadThreshold
			if n.IsClient(*nid) {
				// we have a passive client
				for _, newPrefix := range ncfg.Prefixes {
					recentlyAdvertised := r.hasRecentlyAdvertised(newPrefix.GetPrefix())
					if recentlyUpdated || !hasOtherAdvertisers && recentlyAdvertised {
						r.updatePassiveClient(n, newPrefix, *nid, !recentlyUpdated)
					}
				}
			}
		}
	}
	return nil
}
