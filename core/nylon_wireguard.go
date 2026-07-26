package core

import (
	"bufio"
	"cmp"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"runtime"
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
	n.Interface = itfName

	n.InstallTC()
	n.Log.Info("installed nylon traffic control filter for polysock")

	dev.IpcHandler["get=nylon\n"] = func(writer *bufio.ReadWriter) error {
		return HandleNylonIPC(n, writer)
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
	err = n.SyncWireGuard()
	if err != nil {
		return err
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
			} else if !slices.Contains(n.AppliedSystem.Aliases, addr) {
				n.AppliedSystem.Aliases = append(n.AppliedSystem.Aliases, addr)
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

	// schedule application state reconciliation
	n.RepeatTask(func() error {
		if err := n.SyncApplicationState(); err != nil {
			n.Log.Warn("runtime reconciliation incomplete; will retry", "err", err)
		}
		return nil
	}, n.ProbeDelay)

	return nil
}

func (n *Nylon) cleanupWireGuard() error {
	// remove routes
	for _, route := range n.AppliedSystem.Routes {
		err := RemoveRoute(n.Log, n.Tun, n.Interface, route)
		if err != nil {
			n.Log.Error("failed to remove route", "err", err)
		}
	}
	for _, addr := range n.AppliedSystem.Aliases {
		err := RemoveAlias(n.Log, n.Interface, addr)
		if err != nil {
			n.Log.Error("failed to remove alias", "err", err)
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

func (n *Nylon) SyncWireGuard() error {
	if n.Device == nil {
		return nil
	}
	if n.AppliedSystem.Peers == nil {
		n.AppliedSystem.Peers = make(map[state.NodeId]state.NyPublicKey)
	}

	desired := make(map[state.NodeId]state.NyPublicKey)
	for _, peer := range n.GetPeers(n.LocalCfg.Id) {
		ncfg := n.GetNode(peer)
		desired[peer] = ncfg.PubKey
	}

	// Prepare every desired peer before removing any old peer. In particular,
	// public-key rotation must keep the old peer alive until the forwarding
	// table has been rebound to the replacement.
	for _, peer := range slices.Sorted(slices.Values(n.GetPeers(n.LocalCfg.Id))) {
		ncfg := n.GetNode(peer)
		wgPeer := n.Device.LookupPeer(device.NoisePublicKey(ncfg.PubKey))
		if wgPeer == nil {
			n.Log.Debug("adding", "peer", peer)
			var err error
			wgPeer, err = n.Device.NewPeer(device.NoisePublicKey(ncfg.PubKey))
			if err != nil {
				return err
			}
			wgPeer.Start()
		}
		if n.IsClient(peer) {
			wgPeer.SetPreferRoaming(true)
		}
	}

	if err := n.syncWireGuardEndpoints(); err != nil {
		return err
	}

	// The forwarding table caches concrete peers for the one-lookup hot path.
	// Rebind every entry before stopping peers from the previous generation.
	n.rebindForwardingPeers()

	desiredKeys := make(map[state.NyPublicKey]struct{}, len(desired))
	for _, key := range desired {
		desiredKeys[key] = struct{}{}
	}
	for _, wgPeer := range n.Device.GetPeers() {
		key := state.NyPublicKey(wgPeer.GetPublicKey())
		if _, ok := desiredKeys[key]; ok {
			continue
		}
		n.Log.Debug("removing obsolete WireGuard peer", "key", key)
		n.Device.RemovePeer(wgPeer.GetPublicKey())
	}

	n.AppliedSystem.Peers = desired
	return nil
}

func (n *Nylon) syncWireGuardEndpoints() error {
	if n.Device == nil {
		return nil
	}
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
				nep, err := ep.AsNylonEndpoint().GetWgEndpoint(n.Device, n.EndpointResolver)
				if err != nil {
					continue
				}
				eps = append(eps, nep)
			}
		}

		wgPeer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		if wgPeer != nil {
			wgPeer.SetEndpoints(eps)
		}
	}

	return nil
}

func (n *Nylon) SyncSystemState() error {
	if n.NoNetConfigure {
		return nil
	}
	return errors.Join(n.syncAliases(), n.syncSystemRoutes())
}

func (n *Nylon) syncAliases() error {
	desired := n.GetRouter(n.LocalCfg.Id).Addresses
	applied := slices.Clone(n.AppliedSystem.Aliases)
	var syncErr error
	// we must first add the new alias before removing the old ones, else the system might flush our routes
	for _, newEntry := range desired {
		if !slices.Contains(applied, newEntry) {
			n.Log.Debug("installing alias", "addr", newEntry.String())
			err := ConfigureAlias(n.Log, n.Interface, newEntry)
			if err != nil {
				n.Log.Error("failed to configure alias", "err", err)
				syncErr = errors.Join(syncErr, fmt.Errorf("install alias %s: %w", newEntry, err))
				continue
			}
			applied = append(applied, newEntry)
		}
	}
	hadAliases := len(applied) != 0
	for _, oldEntry := range slices.Clone(applied) {
		if !slices.Contains(desired, oldEntry) {
			n.Log.Debug("removing old alias", "addr", oldEntry.String())
			err := RemoveAlias(n.Log, n.Interface, oldEntry)
			if err != nil {
				n.Log.Error("failed to remove alias", "err", err)
				syncErr = errors.Join(syncErr, fmt.Errorf("remove alias %s: %w", oldEntry, err))
				continue
			}
			applied = slices.DeleteFunc(applied, func(addr netip.Addr) bool {
				return addr == oldEntry
			})
		}
	}
	// special case for linux: if all aliases are removed, the kernel will also flush the routes
	if hadAliases && len(applied) == 0 && runtime.GOOS == "linux" {
		n.AppliedSystem.Routes = nil
	}
	n.AppliedSystem.Aliases = applied
	return syncErr
}

func (n *Nylon) syncSystemRoutes() error {
	newEntries := n.ComputeSysRouteTable()
	applied := slices.Clone(n.AppliedSystem.Routes)
	var syncErr error
	// Install new routes before removing old ones so a partial reconciliation
	// preserves as much connectivity as possible.
	for _, newEntry := range newEntries {
		if !slices.Contains(applied, newEntry) {
			// install route
			n.Log.Debug("installing new route", "prefix", newEntry.String())
			err := ConfigureRoute(n.Log, n.Tun, n.Interface, newEntry)
			if err != nil {
				n.Log.Error("failed to configure route", "err", err)
				syncErr = errors.Join(syncErr, fmt.Errorf("install route %s: %w", newEntry, err))
				continue
			}
			applied = append(applied, newEntry)
		}
	}
	for _, oldEntry := range slices.Clone(applied) {
		if !slices.Contains(newEntries, oldEntry) {
			// uninstall route
			n.Log.Debug("removing old route", "prefix", oldEntry.String())
			err := RemoveRoute(n.Log, n.Tun, n.Interface, oldEntry)
			if err != nil {
				n.Log.Error("failed to remove route", "err", err)
				syncErr = errors.Join(syncErr, fmt.Errorf("remove route %s: %w", oldEntry, err))
				continue
			}
			applied = slices.DeleteFunc(applied, func(prefix netip.Prefix) bool {
				return prefix == oldEntry
			})
		}
	}
	n.AppliedSystem.Routes = applied
	return syncErr
}
