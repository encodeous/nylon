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

func (n *Nylon) initWireGuard() error {
	dev, tdev, itfName, err := NewWireGuardDevice(n)
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

	n.InstallTC()
	n.Log.Info("installed nylon traffic control filter for polysock")

	dev.IpcHandler["get=nylon\n"] = func(writer *bufio.ReadWriter) error {
		return HandleNylonIPCGet(n, writer)
	}

	// TODO: fully convert to code-based api
	err = dev.IpcSet(
		fmt.Sprintf(
			`private_key=%s
listen_port=%d
`,
			hex.EncodeToString(n.Key[:]),
			n.Port,
		),
	)
	if err != nil {
		return fmt.Errorf("failed to configure wg device: %v", err)
	}

	// add peers
	peers := n.GetPeers(n.LocalCfg.Id)
	for _, peer := range peers {
		n.Log.Debug("adding", "peer", peer)
		ncfg := n.GetNode(peer)
		wgPeer, err := dev.NewPeer(device.NoisePublicKey(ncfg.PubKey))
		if err != nil {
			return err
		}
		if n.IsClient(peer) {
			wgPeer.SetPreferRoaming(true)
		}

		// seed initial endpoints
		if n.IsClient(peer) {
			wgPeer.Start()
			continue
		}
		rcfg := n.GetRouter(peer)
		endpoints := make([]conn.Endpoint, 0)
		for _, nep := range rcfg.Endpoints {
			ap, err := nep.Get()
			if err != nil {
				continue
			}
			endpoint, err := n.Device.Bind().ParseEndpoint(ap.String())
			if err != nil {
				return err
			}
			endpoints = append(endpoints, endpoint)
		}
		wgPeer.SetEndpoints(endpoints)

		wgPeer.Start()
	}

	// configure system networking

	// run pre-up commands
	for _, cmd := range n.PreUp {
		err = ExecSplit(n.Log, cmd)
		if err != nil {
			n.Log.Error("failed to run pre-up command", "err", err)
		}
	}

	if !n.NoNetConfigure {
		for _, addr := range n.GetRouter(n.LocalCfg.Id).Addresses {
			err := ConfigureAlias(n.Log, itfName, addr)
			if err != nil {
				n.Log.Error("failed to configure alias", "err", err)
			}
		}

		err = InitInterface(n.Log, itfName)
		if err != nil {
			return err
		}
	}

	// run post-up commands
	for _, cmd := range n.PostUp {
		err = ExecSplit(n.Log, cmd)
		if err != nil {
			n.Log.Error("failed to run post-up command", "err", err)
		}
	}

	// init wireguard related tasks
	n.RepeatTask(func() error {
		return UpdateWireGuard(n)
	}, state.ProbeDelay)

	return nil
}

func (n *Nylon) cleanupWireGuard() error {
	// remove routes
	for _, route := range n.prevInstalledRoutes {
		err := RemoveRoute(n.Log, n.Tun, n.itfName, route)
		if err != nil {
			n.Log.Error("failed to remove route", "err", err)
		}
	}
	// run pre-down commands
	for _, cmd := range n.PreDown {
		err := ExecSplit(n.Log, cmd)
		if err != nil {
			n.Log.Error("failed to run pre-down command", "err", err)
		}
	}
	err := CleanupWireGuardDevice(n)
	if err != nil {
		return err
	}
	// run post-down commands
	for _, cmd := range n.PostDown {
		err = ExecSplit(n.Log, cmd)
		if err != nil {
			n.Log.Error("failed to run post-down command", "err", err)
		}
	}
	return nil
}

func UpdateWireGuard(n *Nylon) error {
	dev := n.Device

	// configure endpoints
	for _, peer := range slices.Sorted(slices.Values(n.GetPeers(n.LocalCfg.Id))) {
		if n.IsClient(peer) {
			continue
		}
		pcfg := n.GetRouter(peer)
		nhNeigh := n.RouterState.GetNeighbour(peer)
		eps := make([]conn.Endpoint, 0)

		if nhNeigh != nil {
			links := slices.Clone(nhNeigh.Eps)
			slices.SortStableFunc(links, func(a, b state.Endpoint) int {
				return cmp.Compare(a.Metric(), b.Metric())
			})
			for _, ep := range links {
				nep, err := ep.AsNylonEndpoint().GetWgEndpoint(n.Device)
				if err != nil {
					continue
				}
				eps = append(eps, nep)
			}
		}

		// add endpoint if it is not in the list
		for _, ep := range pcfg.Endpoints {
			ap, err := ep.Get()
			if err != nil {
				continue
			}
			if !slices.ContainsFunc(eps, func(endpoint conn.Endpoint) bool {
				return endpoint.DstIPPort() == ap
			}) {
				endpoint, err := n.Device.Bind().ParseEndpoint(ap.String())
				if err != nil {
					return err
				}
				eps = append(eps, endpoint)
			}
		}

		wgPeer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		wgPeer.SetEndpoints(eps)
	}

	// configure changed route table entries
	if !n.NoNetConfigure {
		router := n.Router
		newEntries := router.ComputeSysRouteTable()
		oldEntries := n.prevInstalledRoutes
		for _, oldEntry := range oldEntries {
			if !slices.Contains(newEntries, oldEntry) {
				// uninstall route
				n.Log.Debug("removing old route", "prefix", oldEntry.String())
				err := RemoveRoute(n.Log, n.Tun, n.itfName, oldEntry)
				if err != nil {
					n.Log.Error("failed to remove route", "err", err)
				}
			}
		}
		for _, newEntry := range newEntries {
			if !slices.Contains(oldEntries, newEntry) {
				// install route
				n.Log.Debug("installing new route", "prefix", newEntry.String())
				err := ConfigureRoute(n.Log, n.Tun, n.itfName, newEntry)
				if err != nil {
					n.Log.Error("failed to configure route", "err", err)
				}
			}
		}
		n.prevInstalledRoutes = newEntries
	}
	return nil
}
