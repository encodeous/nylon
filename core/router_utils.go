package core

import (
	"net/netip"

	"github.com/encodeous/nylon/state"
)

func AddrToPrefix(addr netip.Addr) netip.Prefix {
	res, err := addr.Prefix(addr.BitLen())
	if err != nil {
		panic(err)
	}
	return res
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
