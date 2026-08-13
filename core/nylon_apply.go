package core

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"

	"github.com/encodeous/nylon/state"
)

type ApplyResult string

const (
	ApplyNoop            ApplyResult = "noop"
	ApplyApplied         ApplyResult = "applied"
	ApplyRejected        ApplyResult = "rejected"
	ApplyRestartRequired ApplyResult = "restart_required"
)

func (n *Nylon) ApplyCentralConfig(cfg *state.CentralCfg) (ApplyResult, error) {
	candidate, err := normalizeCentralConfig(cfg)
	if err != nil {
		return ApplyRejected, err
	}
	if !candidate.IsRouter(n.LocalCfg.Id) {
		return ApplyRestartRequired, errors.New("local node is not a router in the new central config")
	}
	sameConfig := reflect.DeepEqual(&n.CentralCfg, candidate)
	if sameConfig {
		// A previous apply may have committed the desired config while external
		// reconciliation was incomplete. Make an explicit retry useful even
		// before the periodic reconciler runs again.
		return ApplyNoop, n.SyncApplicationState()
	}
	if err := n.reconcileRouterState(candidate); err != nil {
		return ApplyRejected, err
	}
	n.reconcileAdvertisedPrefixes(candidate)
	n.CentralCfg = *candidate
	n.updateConfigPollDelay(candidate)

	// From here on the candidate is the accepted desired state. Failures while
	// converging WireGuard or OS state are retryable and must not describe the
	// already-committed config as rejected.
	return ApplyApplied, n.SyncApplicationState()
}

func (n *Nylon) SyncApplicationState() error {
	if n.Device == nil {
		return nil
	}
	if err := n.SyncWireGuard(); err != nil {
		return err
	}
	ComputeRoutes(n.RouterState, n)
	return n.SyncSystemState()
}

func normalizeCentralConfig(cfg *state.CentralCfg) (*state.CentralCfg, error) {
	err, normalized := cfg.Clone()
	if err != nil {
		return nil, err
	}
	state.ExpandCentralConfig(normalized)
	if err := state.CentralConfigValidator(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func (n *Nylon) reconcileRouterState(next *state.CentralCfg) error {
	desired := make(map[state.NodeId]state.RouterCfg)
	for _, peer := range next.GetPeers(n.LocalCfg.Id) {
		if !next.IsRouter(peer) {
			continue
		}
		desired[peer] = next.GetRouter(peer)
	}

	neighs := make([]*state.Neighbour, 0, len(desired))
	for _, neigh := range n.RouterState.Neighbours {
		cfg, ok := desired[neigh.Id]
		if !ok {
			// remove old neighbours
			delete(n.router.IO, neigh.Id)
			continue
		}
		// configure existing neighbours
		reconcileConfiguredEndpoints(neigh, cfg.Endpoints, &n.RouterTunables)
		neighs = append(neighs, neigh)
		delete(desired, neigh.Id)
	}

	// create new neighbours
	ids := make([]state.NodeId, 0, len(desired))
	for id := range desired {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		cfg := desired[id]
		stNeigh := &state.Neighbour{
			Id:     id,
			Routes: make(map[netip.Prefix]state.NeighRoute),
			Eps:    make([]state.Endpoint, 0, len(cfg.Endpoints)),
		}
		for _, ep := range cfg.Endpoints {
			stNeigh.Eps = append(stNeigh.Eps, state.NewEndpoint(ep, false, nil, &n.RouterTunables))
		}
		neighs = append(neighs, stNeigh)
	}
	n.RouterState.Neighbours = neighs
	if n.EndpointResolver != nil {
		addresses := make(map[string]struct{})
		for _, neigh := range neighs {
			for _, endpoint := range neigh.Eps {
				addresses[endpoint.AsNylonEndpoint().Address] = struct{}{}
			}
		}
		n.EndpointResolver.Retain(addresses)
	}

	// rebuild pubkey to peer's id mapping
	pubkeyMap := make(map[state.NyPublicKey]state.NodeId)
	for _, x := range next.Routers {
		pubkeyMap[x.PubKey] = x.Id
	}
	for _, x := range next.Clients {
		pubkeyMap[x.PubKey] = x.Id
	}
	n.PeerMap.Store(new(pubkeyMap))
	return nil
}

func reconcileConfiguredEndpoints(neigh *state.Neighbour, desired []string, t *state.RouterTunables) {
	desiredAddresses := make(map[string]struct{}, len(desired))
	for _, address := range desired {
		desiredAddresses[address] = struct{}{}
	}

	eps := make([]state.Endpoint, 0, len(neigh.Eps)+len(desired))
	seen := make(map[string]struct{}, len(desired))
	for _, ep := range neigh.Eps {
		nep := ep.AsNylonEndpoint()
		if ep.IsRemote() {
			eps = append(eps, ep)
			continue
		}
		// only keep if desired
		if _, ok := desiredAddresses[nep.Address]; ok {
			eps = append(eps, ep)
			seen[nep.Address] = struct{}{}
		}
	}
	for _, address := range desired {
		if _, ok := seen[address]; ok {
			continue
		}
		eps = append(eps, state.NewEndpoint(address, false, nil, t))
	}
	neigh.Eps = eps
}
