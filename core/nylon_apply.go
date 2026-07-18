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

func (n *Nylon) ApplyCentralConfig(cfg *state.CentralCfg) (ApplyResult, error) {
	err, next := cfg.Clone()
	if err != nil {
		return ApplyRejected, err
	}
	state.ExpandCentralConfig(next)
	if err := state.CentralConfigValidator(next); err != nil {
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
	n.CentralCfg = *next

	if err := n.SyncWireGuard(); err != nil {
		return ApplyRejected, err
	}
	if err := n.SyncSystemState(); err != nil {
		return ApplyRejected, err
	}
	ComputeRoutes(n.RouterState, n)

	return ApplyApplied, nil
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
		reconcileConfiguredEndpoints(neigh, configuredEndpoints(n.LocalCfg.Binds, cfg.Endpoints, &n.RouterTunables))
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
		stNeigh.Eps = append(stNeigh.Eps, configuredEndpoints(n.LocalCfg.Binds, cfg.Endpoints, &n.RouterTunables)...)
		neighs = append(neighs, stNeigh)
	}
	n.RouterState.Neighbours = neighs

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

func configuredEndpoints(binds []state.LocalBind, endpoints []*state.DynamicEndpoint, t *state.RouterTunables) []state.Endpoint {
	if len(binds) == 0 {
		eps := make([]state.Endpoint, 0, len(endpoints))
		for _, ep := range endpoints {
			eps = append(eps, state.NewEndpoint(ep, false, nil, t))
		}
		return eps
	}

	eps := make([]state.Endpoint, 0, len(endpoints)*len(binds))
	for _, ep := range endpoints {
		for _, bind := range binds {
			if !bindMatchesEndpoint(bind, ep) {
				continue
			}
			nep := state.NewEndpoint(ep, false, nil, t)
			nep.Bind = bind
			eps = append(eps, nep)
		}
	}
	return eps
}

func bindMatchesEndpoint(bind state.LocalBind, ep *state.DynamicEndpoint) bool {
	if !bind.Source.IsValid() {
		return true
	}
	host, _, err := ep.Parse()
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return true
	}
	return state.SameIPFamily(bind.Source, addr)
}

type endpointIdentity struct {
	value string
	bind  state.LocalBind
}

func endpointKey(ep *state.NylonEndpoint) endpointIdentity {
	return endpointIdentity{
		value: ep.DynEP.Value,
		bind:  ep.Bind,
	}
}

func reconcileConfiguredEndpoints(neigh *state.Neighbour, desired []state.Endpoint) {
	desiredByKey := make(map[endpointIdentity]state.Endpoint, len(desired))
	for _, ep := range desired {
		desiredByKey[endpointKey(ep.AsNylonEndpoint())] = ep
	}

	eps := make([]state.Endpoint, 0, len(neigh.Eps)+len(desired))
	seen := make(map[endpointIdentity]struct{}, len(desired))
	for _, ep := range neigh.Eps {
		nep := ep.AsNylonEndpoint()
		if ep.IsRemote() {
			eps = append(eps, ep)
			continue
		}
		// only keep if desired
		key := endpointKey(nep)
		if _, ok := desiredByKey[key]; ok {
			eps = append(eps, ep)
			seen[key] = struct{}{}
		}
	}
	for _, ep := range desired {
		key := endpointKey(ep.AsNylonEndpoint())
		if _, ok := seen[key]; ok {
			continue
		}
		eps = append(eps, ep)
	}
	neigh.Eps = eps
}

func (n *Nylon) reconcileAdvertisedPrefixes(next *state.CentralCfg) {
	currentLocal := make(map[netip.Prefix]state.PrefixHealthWrapper)
	if cur := n.CentralCfg.TryGetNode(n.LocalCfg.Id); cur != nil {
		for _, prefix := range cur.Prefixes {
			currentLocal[prefix.GetPrefix()] = prefix
		}
	}
	nextNode := next.TryGetNode(n.LocalCfg.Id)
	if nextNode == nil {
		return
	}

	desiredLocal := make(map[netip.Prefix]int)
	for i, prefix := range nextNode.Prefixes {
		desiredLocal[prefix.GetPrefix()] = i
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

	for prefix, index := range desiredLocal {
		desired := nextNode.Prefixes[index]
		if current, ok := currentLocal[prefix]; ok && current.SameConfig(desired, &n.RouterTunables) {
			desired = current
			nextNode.Prefixes[index] = current
		} else {
			if current, ok := currentLocal[prefix]; ok {
				current.Stop()
			}
			n.Log.Debug("starting prefix healthcheck", "prefix", prefix)
			desired.Start(n.Log, &n.RouterTunables)
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
		n.Log.Debug("starting prefix healthcheck", "prefix", ph.GetPrefix())
		ph.Start(n.Log, &n.RouterTunables)
	}
}

var maxConfigTime = time.Unix(1<<63-62135596801, 999999999)
