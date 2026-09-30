package core

import (
	"maps"
	"slices"
	"time"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/state"
)

func (n *Nylon) initPassiveClient() error {
	n.RepeatTask(func() error {
		return scanPassivePeers(n)
	}, n.ProbeDelay)
	return nil
}

func scanPassivePeers(n *Nylon) error {
	// A node can have a peer on each transport. Use the most recent packet from any of them.
	lastRX := make(map[state.NodeId]time.Time)
	for _, stat := range n.transportPeers() {
		n.setPeerReceived(stat.Peer, stat.LastReceived)
		key, valid := peerPublicKey(stat.Peer)
		if !valid {
			continue
		}
		nid := n.FindNodeBy(key)
		if nid == nil {
			continue
		}
		if last, seen := lastRX[*nid]; !seen || stat.LastReceived.After(last) {
			lastRX[*nid] = stat.LastReceived
		}
	}

	n.publishLinks()

	for _, nid := range slices.Sorted(maps.Keys(lastRX)) {
		if !n.IsClient(nid) {
			continue
		}
		// check if we are the only node that is advertising this passive client, if so, we can apply the following optimization
		// As we are the only node advertising the client, we can permanently hold the route (with a very high metric), and not expire it
		// This enables our passive client to be reachable even if it does not send any traffic for a long time (e.g. mobile device going to sleep)
		// If this device switches to another nylon node, that node will start advertising the client, and we will stop holding the route

		hasOtherAdvertisers := false
		ncfg := n.GetNode(nid)
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

		recentlyUpdated := time.Since(lastRX[nid]) < n.ClientDeadThreshold
		for _, newPrefix := range ncfg.Prefixes {
			recentlyAdvertised := n.hasRecentlyAdvertised(newPrefix.GetPrefix())
			if recentlyUpdated || !hasOtherAdvertisers && recentlyAdvertised {
				n.updatePassiveClient(newPrefix, nid, !recentlyUpdated)
			}
		}
	}
	return nil
}

// passiveClientRoamed forwards to a client through the transport it now uses.
// Clients have no link health, so their links are ranked by recent traffic.
func (n *Nylon) passiveClientRoamed(peer polyamide.Peer) {
	n.setPeerReceived(peer, time.Now())
	n.publishLinks()
}

// setPeerReceived records when peer last received a packet, ignoring older times.
func (n *Nylon) setPeerReceived(peer polyamide.Peer, at time.Time) {
	if n.peerReceived == nil {
		n.peerReceived = make(map[polyamide.Peer]time.Time)
	}
	if at.After(n.peerReceived[peer]) {
		n.peerReceived[peer] = at
	}
}
