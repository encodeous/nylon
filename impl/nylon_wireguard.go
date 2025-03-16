package impl

import (
	"encoding/hex"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"net/netip"
	"slices"
	"sort"
)

func (n *Nylon) initWireGuard(s *state.State) error {
	dev, itfName, err := NewWireGuardDevice(s, n)
	if err != nil {
		return err
	}

	n.PolySock = dev.PolyListen(n)
	s.Log.Info("started polysock listener")

	n.Device = dev
	n.itfName = itfName

	// TODO: fully convert to code-based api
	err = dev.IpcSet(
		fmt.Sprintf(
			`private_key=%s
listen_port=%d
allow_inbound=true
`,
			hex.EncodeToString(s.Key[:]),
			s.Port,
		),
	)
	if err != nil {
		return fmt.Errorf("failed to configure wg device: %v", err)
	}

	// add peers
	peers := s.GetPeers()
	for _, peer := range peers {
		s.Log.Debug("adding", "peer", peer)
		ncfg := s.GetNode(peer)
		wgPeer, err := dev.NewPeer(device.NoisePublicKey(ncfg.PubKey))
		if err != nil {
			return err
		}
		if s.IsClient(peer) {
			wgPeer.SetPreferRoaming(true)
		}

		// seed initial endpoints
		if s.IsClient(peer) {
			continue
		}
		rcfg := s.GetRouter(peer)
		endpoints := make([]conn.Endpoint, 0)
		for _, nep := range rcfg.Endpoints {
			endpoint, err := n.Device.Bind().ParseEndpoint(nep.String())
			if err != nil {
				return err
			}
			endpoints = append(endpoints, endpoint)
		}
		wgPeer.SetEndpoints(endpoints)

		wgPeer.Start()
	}

	// configure system networking

	if !s.LocalCfg.NoNetConfigure {
		selfPrefixes := s.GetRouter(s.Id).Prefixes
		err = InitInterface(itfName)
		if err != nil {
			return err
		}

		if len(selfPrefixes) != 0 {
			// configure system
			// assign ip
			for _, prefix := range selfPrefixes {
				err = ConfigureAlias(itfName, prefix)
				if err != nil {
					return err
				}
			}

			if len(s.LocalCfg.AllowedPrefixes) == 0 {
				for _, peer := range s.CentralCfg.GetNodes() {
					if peer.Id == s.Id {
						continue
					}
					for _, prefix := range peer.Prefixes {
						err = ConfigureRoute(itfName, prefix, selfPrefixes[0].Addr())
						if err != nil {
							return err
						}
					}
				}
			} else {
				for _, prefix := range s.LocalCfg.AllowedPrefixes {
					err = ConfigureRoute(itfName, prefix, selfPrefixes[0].Addr())
					if err != nil {
						return err
					}
				}
			}
		}
	}

	// init wireguard related tasks

	s.RepeatTask(UpdateWireGuard, state.ProbeDelay)

	return nil
}

func (n *Nylon) cleanupWireGuard(s *state.State) error {
	return CleanupWireGuardDevice(s, n)
}

func UpdateWireGuard(s *state.State) error {
	r := Get[*Router](s)
	n := Get[*Nylon](s)
	dev := n.Device

	routesToNeigh := make(map[state.NodeId][]*state.Route)
	for _, route := range r.Routes {
		routesToNeigh[route.Nh] = append(routesToNeigh[route.Nh], route)
	}

	// configure peers/routing
	for neigh, routes := range routesToNeigh {
		if neigh == s.Id {
			// set client allowedIps individually
			for _, route := range routes {
				nid := route.Src.Id
				if s.IsClient(nid) {
					ccfg := s.GetClient(nid)
					peer := dev.LookupPeer(device.NoisePublicKey(ccfg.PubKey))
					for _, prefix := range ccfg.Prefixes {
						dev.Allowedips.Insert(prefix, peer)
					}
				}
			}
		} else {
			allowedIps := make([]string, 0)
			pcfg := s.GetNode(neigh)
			for _, route := range routes {
				cfg := s.TryGetNode(route.Src.Id)
				if cfg == nil {
					continue // config might not always be synced
				}
				for _, prefix := range cfg.Prefixes {
					allowedIps = append(allowedIps, prefix.String())
				}
			}
			sort.Strings(allowedIps)

			peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
			for _, allowedIp := range allowedIps {
				dev.Allowedips.Insert(netip.MustParsePrefix(allowedIp), peer)
			}
		}
	}

	// configure endpoints
	for _, peer := range slices.Sorted(slices.Values(s.GetPeers())) {
		if s.IsClient(peer) {
			continue
		}
		pcfg := s.GetRouter(peer)
		nhNeigh := s.GetNeighbour(peer)
		eps := make([]conn.Endpoint, 0)

		if nhNeigh != nil {
			links := slices.Clone(nhNeigh.Eps)
			slices.SortStableFunc(links, func(a, b *state.DynamicEndpoint) int {
				return -int(a.Metric() - b.Metric())
			})
			for _, ep := range links {
				eps = append(eps, ep.NetworkEndpoint().GetWgEndpoint(n.Device))
			}
		}

		// add endpoint if it is not in the list
		for _, ep := range pcfg.Endpoints {
			if !slices.ContainsFunc(eps, func(endpoint conn.Endpoint) bool {
				return endpoint.DstIPPort() == ep
			}) {
				endpoint, err := n.Device.Bind().ParseEndpoint(ep.String())
				if err != nil {
					return err
				}
				eps = append(eps, endpoint)
			}
		}

		wgPeer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		wgPeer.SetEndpoints(eps)
	}
	return nil
}
