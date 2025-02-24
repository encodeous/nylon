package impl

import (
	"fmt"
	"github.com/encodeous/nylon/nylon_dp"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"net"
	"net/netip"
	"slices"
	"sort"
)

func (n *Nylon) initWireGuard(s *state.State) error {
	s.Log.Info("initializing Data Plane")

	dp, err := nylon_dp.NewItf(s)
	if err != nil {
		return fmt.Errorf("could not initialize interface: %v", err)
	}
	n.dataplane = dp

	wgDev := dp.GetDevice()

	n.PolySock = wgDev.PolyListen(n)
	n.WgDevice = wgDev
	s.Log.Info("started polysock listener")

	s.RepeatTask(UpdateWireGuard, state.ProbeDpDelay)
	return nil
}

func (n *Nylon) cleanupWireGuard(s *state.State) error {
	return n.dataplane.Cleanup(s)
}

func UpdateWireGuard(s *state.State) error {
	r := Get[*Router](s)
	n := Get[*Nylon](s)
	dev := n.WgDevice

	routesToNeigh := make(map[state.Node][]*state.Route)
	for _, route := range r.Routes {
		routesToNeigh[route.Nh] = append(routesToNeigh[route.Nh], route)
	}

	// configure peers
	for neigh, routes := range routesToNeigh {
		pcfg := s.MustGetNode(neigh)
		allowedIps := make([]string, 0)
		for _, route := range routes {
			cfg, err := s.GetPubNodeCfg(route.Src.Id)
			if err != nil {
				continue
			}
			_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", cfg.NylonAddr, cfg.NylonAddr.BitLen())) // TODO: add support for prefixes
			if err != nil {
				continue
			}
			allowedIps = append(allowedIps, ipNet.String())
		}
		sort.Strings(allowedIps)

		peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		for _, allowedIp := range allowedIps {
			dev.Allowedips.Insert(netip.MustParsePrefix(allowedIp), peer)
		}
	}

	for _, neigh := range s.GetPeers() {
		pcfg := s.MustGetNode(neigh)
		nhNeigh := s.GetNeighbour(neigh)
		eps := make([]conn.Endpoint, 0)

		if nhNeigh != nil {
			links := slices.Clone(nhNeigh.Eps)
			slices.SortStableFunc(links, func(a, b *state.DynamicEndpoint) int {
				return -int(a.Metric() - b.Metric())
			})
			for _, ep := range links {
				eps = append(eps, ep.NetworkEndpoint().GetWgEndpoint())
			}
		}

		// add endpoint if it is not in the list
		for _, ep := range pcfg.DpAddr {
			if !slices.Contains(eps, ep.GetWgEndpoint()) {
				eps = append(eps, ep.GetWgEndpoint())
			}
		}

		peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		peer.SetEndpoints(eps)
	}
	return nil
}
