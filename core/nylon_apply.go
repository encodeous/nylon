package core

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"time"

	"github.com/encodeous/nylon/state"
)

type ApplyResult string

const (
	ApplyNoop            ApplyResult = "noop"
	ApplyApplied         ApplyResult = "applied"
	ApplyRejected        ApplyResult = "rejected"
	ApplyRestartRequired ApplyResult = "restart_required"
)

func (n *Nylon) ApplyCentralConfig(next state.CentralCfg) (ApplyResult, error) {
	state.ExpandCentralConfig(&next)
	if err := state.CentralConfigValidator(&next); err != nil {
		return ApplyRejected, err
	}
	if !next.IsRouter(n.LocalCfg.Id) {
		return ApplyRestartRequired, errors.New("local node is not a router in the new central config")
	}
	if reflect.DeepEqual(n.CentralCfg, next) {
		return ApplyNoop, nil
	}

	if err := n.reconcileRouterState(next); err != nil {
		return ApplyRejected, err
	}
	n.reconcileAdvertisedPrefixes(next)
	n.CentralCfg = next

	if err := n.SyncWireGuard(); err != nil {
		return ApplyRejected, err
	}
	if err := n.SyncSystemState(); err != nil {
		return ApplyRejected, err
	}
	ComputeRoutes(n.RouterState, n)

	return ApplyApplied, nil
}

func (n *Nylon) reconcileRouterState(next state.CentralCfg) error {
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
			delete(n.router.IO, neigh.Id)
			continue
		}
		reconcileConfiguredEndpoints(neigh, cfg.Endpoints)
		neighs = append(neighs, neigh)
		delete(desired, neigh.Id)
	}

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
			stNeigh.Eps = append(stNeigh.Eps, state.NewEndpoint(ep, false, nil))
		}
		neighs = append(neighs, stNeigh)
	}
	n.RouterState.Neighbours = neighs
	return nil
}

func reconcileConfiguredEndpoints(neigh *state.Neighbour, desired []*state.DynamicEndpoint) {
	desiredByValue := make(map[string]*state.DynamicEndpoint, len(desired))
	for _, ep := range desired {
		desiredByValue[ep.Value] = ep
	}

	eps := make([]state.Endpoint, 0, len(neigh.Eps)+len(desired))
	seen := make(map[string]struct{}, len(desired))
	for _, ep := range neigh.Eps {
		nep := ep.AsNylonEndpoint()
		if ep.IsRemote() {
			eps = append(eps, ep)
			continue
		}
		if desiredEp, ok := desiredByValue[nep.DynEP.Value]; ok {
			nep.DynEP = desiredEp
			eps = append(eps, ep)
			seen[desiredEp.Value] = struct{}{}
		}
	}
	for _, ep := range desired {
		if _, ok := seen[ep.Value]; ok {
			continue
		}
		eps = append(eps, state.NewEndpoint(ep, false, nil))
	}
	neigh.Eps = eps
}

func (n *Nylon) reconcileAdvertisedPrefixes(next state.CentralCfg) {
	cur := n.GetRouter(n.LocalCfg.Id)
	nextRouter := next.GetRouter(n.LocalCfg.Id)

	currentLocal := make(map[netip.Prefix]state.PrefixHealthWrapper)
	for _, prefix := range cur.Prefixes {
		currentLocal[prefix.GetPrefix()] = prefix
	}
	desiredLocal := make(map[netip.Prefix]state.PrefixHealthWrapper)
	for _, prefix := range nextRouter.Prefixes {
		desiredLocal[prefix.GetPrefix()] = prefix
	}

	for prefix, adv := range n.RouterState.Advertised {
		if adv.NodeId != n.LocalCfg.Id {
			continue
		}
		if _, ok := desiredLocal[prefix]; !ok {
			if old, ok := currentLocal[prefix]; ok {
				old.Stop()
			}
			delete(n.RouterState.Advertised, prefix)
		}
	}

	for prefix, desired := range desiredLocal {
		if _, ok := currentLocal[prefix]; !ok {
			n.Log.Info("starting prefix healthcheck", "prefix", prefix)
			desired.Start(n.Log)
		}
		n.RouterState.Advertised[prefix] = state.Advertisement{
			NodeId:   n.LocalCfg.Id,
			Expiry:   maxConfigTime,
			MetricFn: desired.GetMetric,
			ExpiryFn: func() {
				desired.Stop()
			},
		}
	}
}

func (n *Nylon) startAdvertisedPrefixHealth() {
	for _, ph := range n.GetNode(n.LocalCfg.Id).Prefixes {
		n.Log.Info("starting prefix healthcheck", "prefix", ph.GetPrefix())
		ph.Start(n.Log)
	}
}

var maxConfigTime = time.Unix(1<<63-62135596801, 999999999)
