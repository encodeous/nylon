package core

import (
	"errors"
	"fmt"
	"net/netip"
	"runtime"
	"slices"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/state"
)

func (n *Nylon) initTransport() error {
	if len(n.Transports) == 0 {
		rt, err := newDefaultRuntime(n)
		if err != nil {
			return err
		}
		if err := rt.validate(); err != nil {
			rt.close()
			return err
		}
		n.useRuntime(rt)
	}
	name, err := n.Tun.Name()
	if err != nil {
		return err
	}
	n.Interface = name
	// Nodes that fail to sync are retried by the reconciliation task below.
	if err := n.SyncTransport(); err != nil {
		n.Log.Warn("initial transport sync incomplete; will retry", "err", err)
	}
	n.applyHostMTU()
	for _, transport := range n.Transports {
		if err := transport.Start(n.Context, n.transportHooks()); err != nil {
			return err
		}
	}
	if n.wireGuard != nil {
		n.wireGuard.serve(n.Context, n.Log)
	}
	n.startTUNReader()
	n.watchTUNEvents()
	// configure system networking

	// run pre-up commands
	for _, cmd := range n.PreUp {
		err = ExecSplit(n.Log, cmd)
		if err != nil {
			n.Log.Error("failed to run pre-up command", "err", err)
		}
	}

	if !n.NoNetConfigure && !n.NoTun {
		for _, addr := range n.GetRouter(n.LocalCfg.Id).Addresses {
			err := ConfigureAlias(n.Log, n.Interface, addr)
			if err != nil {
				n.Log.Error("failed to configure alias", "err", err)
			} else if !slices.Contains(n.AppliedSystem.Aliases, addr) {
				n.AppliedSystem.Aliases = append(n.AppliedSystem.Aliases, addr)
			}
		}

		err = InitInterface(n.Log, n.Interface)
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

func (n *Nylon) cleanupTransport() error {
	for _, route := range n.AppliedSystem.Routes {
		if err := RemoveRoute(n.Log, n.Tun, n.Interface, route); err != nil {
			n.Log.Error("failed to remove route", "err", err)
		}
	}
	for _, addr := range n.AppliedSystem.Aliases {
		if err := RemoveAlias(n.Log, n.Interface, addr); err != nil {
			n.Log.Error("failed to remove alias", "err", err)
		}
	}
	for _, cmd := range n.PreDown {
		if err := ExecSplit(n.Log, cmd); err != nil {
			n.Log.Error("failed to run pre-down command", "err", err)
		}
	}
	var err error
	if n.wireGuard != nil {
		err = errors.Join(err, n.wireGuard.close())
	}
	if n.Tun != nil {
		err = errors.Join(err, n.Tun.Close())
	}
	n.tunWorkers.Wait()
	for _, transport := range n.Transports {
		err = errors.Join(err, transport.Close())
	}
	for _, cmd := range n.PostDown {
		if e := ExecSplit(n.Log, cmd); e != nil {
			n.Log.Error("failed to run post-down command", "err", e)
		}
	}
	return err
}

func (n *Nylon) transportPeer(id state.NodeId, transport polyamide.Transport) polyamide.Peer {
	return n.peerHandles[id][transport]
}
func peerPublicKey(peer polyamide.Peer) (state.NyPublicKey, bool) {
	var key state.NyPublicKey
	if peer == nil {
		return key, false
	}
	return state.NyPublicKey(peer.PublicKey()), true
}

func (n *Nylon) SyncSystemState() error {
	if n.NoNetConfigure || n.NoTun {
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

// defaultTransport carries configured endpoints when the runtime supplies no links.
// The platform runtime uses WireGuard.
func (n *Nylon) defaultTransport() polyamide.Transport {
	if len(n.Transports) == 0 {
		return nil
	}
	return n.Transports[0]
}

func (n *Nylon) transportPeers() []polyamide.PeerStats {
	var peers []polyamide.PeerStats
	for _, transport := range n.Transports {
		peers = append(peers, transport.Peers()...)
	}
	return peers
}
