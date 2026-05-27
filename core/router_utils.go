package core

import (
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/state"
)

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

// ForwardEntryToNode looks up the best route to a given node and returns a
// RouteTableEntry pointing at the next-hop peer. Returns (entry, true) if a
// non-self, finite-metric route exists; (zero, false) otherwise.
//
// This is used by the exit-encap and exit-transit filters to address an
// entire node (rather than a prefix); the prefix-keyed ForwardTable does not
// answer that question directly because a node may not advertise a single
// prefix that uniquely identifies it. Must be called from the dispatch
// goroutine — it touches RouterState.Routes which is not atomic.
func (n *Nylon) ForwardEntryToNode(node state.NodeId) (RouteTableEntry, bool) {
	if node == n.LocalCfg.Id {
		return RouteTableEntry{Nh: n.LocalCfg.Id}, true
	}

	var best state.SelRoute
	found := false
	for _, route := range n.RouterState.Routes {
		if route.NodeId != node || route.Nh == n.LocalCfg.Id || route.Metric == state.INF {
			continue
		}
		if !found || route.Metric < best.Metric {
			best = route
			found = true
		}
	}
	if !found {
		return RouteTableEntry{}, false
	}

	peer := n.Device.LookupPeer(device.NoisePublicKey(n.GetNode(best.Nh).PubKey))
	if peer == nil {
		return RouteTableEntry{}, false
	}
	return RouteTableEntry{
		Nh:   best.Nh,
		Peer: peer,
	}, true
}
