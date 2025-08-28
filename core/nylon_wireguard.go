package core

import (
	"cmp"
	"encoding/hex"
	"fmt"
	"net/netip"
	"slices"

	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/state"
)

func (n *Nylon) initWireGuard(s *state.State) error {
	dev, tdev, itfName, err := NewWireGuardDevice(s, n)
	if err != nil {
		return err
	}

	err = dev.Up()
	if err != nil {
		return err
	}

	n.Device = dev
	n.Tun = tdev
	n.itfName = itfName

	n.InstallTC(s)
	s.Log.Info("installed nylon traffic control filter for polysock")

	// TODO: fully convert to code-based api
	err = dev.IpcSet(
		fmt.Sprintf(
			`private_key=%s
listen_port=%d
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
			wgPeer.Start()
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

	if !s.NoNetConfigure {
		// configure self
		selfSvc := make(map[state.ServiceId]struct{})

		var defaultAddr *netip.Addr
		for _, svc := range s.GetRouter(s.Id).Services {
			prefix := s.GetSvcPrefix(svc)
			if defaultAddr == nil {
				addr := prefix.Addr()
				defaultAddr = &addr
			}
			selfSvc[svc] = struct{}{}
			err = ConfigureAlias(itfName, prefix)
			if err != nil {
				return err
			}
		}

		if defaultAddr == nil {
			return fmt.Errorf("no address configured for self")
		}

		err = InitInterface(itfName)

		if err != nil {
			return err
		}

		// configure services
		for svc, prefix := range s.Services {
			if _, ok := selfSvc[svc]; ok {
				continue
			}
			err = ConfigureRoute(n.Tun, itfName, prefix, *defaultAddr)
			if err != nil {
				return err
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
	n := Get[*Nylon](s)
	dev := n.Device

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
			slices.SortStableFunc(links, func(a, b state.Endpoint) int {
				return cmp.Compare(a.Metric(), b.Metric())
			})
			for _, ep := range links {
				eps = append(eps, ep.AsNylonEndpoint().GetWgEndpoint(n.Device))
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
