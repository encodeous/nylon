package core

import (
	"bufio"
	"cmp"
	"encoding/hex"
	"fmt"
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

	dev.IpcHandler["get=nylon\n"] = func(writer *bufio.ReadWriter) error {
		return HandleNylonIPCGet(s, writer)
	}

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
	peers := s.GetPeers(s.Id)
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
		// run pre-up commands
		for _, cmd := range s.PreUp {
			err = ExecSplit(s.Log, cmd)
			if err != nil {
				s.Log.Error("failed to run pre-up command", "err", err)
			}
		}

		for _, addr := range s.GetRouter(s.Id).Addresses {
			err := ConfigureAlias(s.Log, itfName, addr)
			if err != nil {
				s.Log.Error("failed to configure alias", "err", err)
			}
		}

		err = InitInterface(s.Log, itfName)

		if err != nil {
			return err
		}

		// configure prefixes
		exclude := append(state.SubtractPrefix(s.CentralCfg.ExcludeIPs, s.IncludeIPs), s.LocalCfg.ExcludeIPs...)
		for _, excl := range exclude {
			s.Log.Debug("Computed Exclude Prefix", "prefix", excl.String())
		}
		computed := state.SubtractPrefix(s.GetPrefixes(), exclude)
		for _, pre := range computed {
			s.Log.Debug("Computed Prefix", "prefix", pre.String())
		}
		for _, prefix := range computed {
			err := ConfigureRoute(s.Log, n.Tun, itfName, prefix)
			if err != nil {
				s.Log.Error("failed to configure route", "err", err)
			}
		}

		// run post-up commands
		for _, cmd := range s.PostUp {
			err = ExecSplit(s.Log, cmd)
			if err != nil {
				s.Log.Error("failed to run post-up command", "err", err)
			}
		}
	}

	// init wireguard related tasks

	s.RepeatTask(UpdateWireGuard, state.ProbeDelay)

	return nil
}

func (n *Nylon) cleanupWireGuard(s *state.State) error {
	// run pre-down commands
	for _, cmd := range s.PreUp {
		err := ExecSplit(s.Log, cmd)
		if err != nil {
			s.Log.Error("failed to run pre-down command", "err", err)
		}
	}
	err := CleanupWireGuardDevice(s, n)
	if err != nil {
		return err
	}
	// run post-down commands
	for _, cmd := range s.PostDown {
		err = ExecSplit(s.Log, cmd)
		if err != nil {
			s.Log.Error("failed to run post-down command", "err", err)
		}
	}
	return nil
}

func UpdateWireGuard(s *state.State) error {
	n := Get[*Nylon](s)
	dev := n.Device

	// configure endpoints
	for _, peer := range slices.Sorted(slices.Values(s.GetPeers(s.Id))) {
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
