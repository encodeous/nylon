package core

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/state"
)

type Link struct {
	Peer     polyamide.Peer
	Endpoint polyamide.Endpoint
}

type LinkConfig struct {
	Transport polyamide.Transport
	// Empty allows the transport to learn the peer's endpoint.
	Address string
}

type neighbourLink struct {
	Link
	health *state.NylonEndpoint
}

// rankLinks orders links by metric. Links without health rank last.
func rankLinks(links []neighbourLink) []neighbourLink {
	metric := func(link neighbourLink) uint32 {
		if link.health == nil {
			return state.INF
		}
		return link.health.Metric()
	}
	ranked := slices.Clone(links)
	slices.SortStableFunc(ranked, func(a, b neighbourLink) int { return cmp.Compare(metric(a), metric(b)) })
	return ranked
}

// forwardingLinks keeps passive links and links with a finite metric.
func (n *Nylon) forwardingLinks() map[state.NodeId][]Link {
	links := make(map[state.NodeId][]Link, len(n.neighbourLinks))
	for id, candidates := range n.neighbourLinks {
		for _, link := range rankLinks(candidates) {
			if link.health == nil || link.health.Metric() != state.INF {
				links[id] = append(links[id], link.Link)
			}
		}
	}
	return links
}

func (n *Nylon) bestLink(id state.NodeId) (Link, bool) {
	tables := n.router.Tables.Load()
	if tables == nil || len(tables.Links[id]) == 0 {
		return Link{}, false
	}
	return tables.Links[id][0], true
}

// SyncTransport binds every peer to its configured links. A node that fails to
// sync keeps its previous bindings, and the other nodes are still synced.
func (n *Nylon) SyncTransport() error {
	if len(n.Transports) == 0 {
		return nil
	}
	ids := slices.Sorted(slices.Values(n.GetPeers(n.LocalCfg.Id)))
	result := n.releaseMovedKeys(ids)
	desired := make(map[state.NodeId]state.NyPublicKey)
	handles := make(map[state.NodeId]map[polyamide.Transport]polyamide.Peer)
	links := make(map[state.NodeId][]neighbourLink)
	for _, id := range ids {
		peers, nodeLinks, err := n.syncNode(id)
		if err != nil {
			result = errors.Join(result, fmt.Errorf("sync neighbour %s: %w", id, err))
			peers, nodeLinks = n.peerHandles[id], n.neighbourLinks[id]
			if key, ok := n.AppliedSystem.Peers[id]; ok {
				desired[id] = key
			}
		} else {
			desired[id] = n.GetNode(id).PubKey
		}
		handles[id], links[id] = peers, nodeLinks
	}
	// Publish the new bindings before retiring old peer handles.
	n.peerHandles, n.neighbourLinks = handles, links
	for _, neigh := range n.RouterState.Neighbours {
		neigh.Eps = nil
		for _, link := range links[neigh.Id] {
			if link.health != nil {
				neigh.Eps = append(neigh.Eps, link.health)
			}
		}
	}
	n.publishLinks()
	for _, stat := range n.transportPeers() {
		if handles[state.NodeId(stat.Peer.ID())][stat.Peer.Transport()] != stat.Peer {
			result = errors.Join(result, stat.Peer.Transport().RemovePeer(n.Context, stat.Peer))
		}
	}
	n.AppliedSystem.Peers = desired
	return result
}

// releaseMovedKeys removes peers whose public key now belongs to a different node,
// so the new node can bind it. Keys claimed by several nodes are left alone.
func (n *Nylon) releaseMovedKeys(ids []state.NodeId) error {
	owners := make(map[state.NyPublicKey][]state.NodeId)
	for _, id := range ids {
		key := n.GetNode(id).PubKey
		owners[key] = append(owners[key], id)
	}
	var result error
	for _, stat := range n.transportPeers() {
		owner := owners[state.NyPublicKey(stat.Peer.PublicKey())]
		if len(owner) == 1 && owner[0] != state.NodeId(stat.Peer.ID()) {
			result = errors.Join(result, stat.Peer.Transport().RemovePeer(n.Context, stat.Peer))
		}
	}
	return result
}

// syncNode prepares a node's peers on each configured transport and gives each
// transport the node's endpoints, best first.
func (n *Nylon) syncNode(id state.NodeId) (map[polyamide.Transport]polyamide.Peer, []neighbourLink, error) {
	cfg := n.GetNode(id)
	peers := make(map[polyamide.Transport]polyamide.Peer)
	var links []neighbourLink
	for _, config := range n.configuredLinks(id) {
		transport := config.Transport
		if transport == nil || !slices.Contains(n.Transports, transport) {
			return nil, nil, errors.New("unregistered transport")
		}
		peer := peers[transport]
		if peer == nil {
			var err error
			peer, err = transport.PreparePeer(n.Context, polyamide.PeerConfig{ID: string(id), PublicKey: polyamide.PublicKey(cfg.PubKey), Passive: n.IsClient(id)})
			if err != nil {
				return nil, nil, err
			}
			peers[transport] = peer
		}
		// Unresolved addresses are prepared when they are probed.
		var endpoint polyamide.Endpoint
		if config.Address != "" {
			if address, err := n.EndpointResolver.Get(config.Address); err == nil {
				// Reuse native state until DNS changes the destination.
				for _, old := range n.neighbourLinks[id] {
					if old.health == config.health && old.Peer.Transport() == transport && old.Endpoint != nil && old.Endpoint.Address() == address.String() {
						endpoint = old.Endpoint
						break
					}
				}
				if endpoint == nil {
					endpoint, err = transport.PrepareEndpoint(n.Context, address.String())
					if err != nil {
						return nil, nil, err
					}
				}
			}
		}
		links = append(links, neighbourLink{Link: Link{Peer: peer, Endpoint: endpoint}, health: config.health})
	}
	// The first endpoint is the transport's default for this peer.
	endpoints := make(map[polyamide.Transport][]polyamide.Endpoint)
	for _, link := range rankLinks(links) {
		if link.Endpoint != nil {
			endpoints[link.Peer.Transport()] = append(endpoints[link.Peer.Transport()], link.Endpoint)
		}
	}
	for transport, peer := range peers {
		if len(endpoints[transport]) == 0 && n.IsClient(id) {
			continue
		}
		if err := transport.SetEndpoints(n.Context, peer, endpoints[transport]); err != nil {
			return nil, nil, err
		}
	}
	return peers, links, nil
}

type configuredLink struct {
	LinkConfig
	health *state.NylonEndpoint
}

// defaultLinks binds the configured endpoints of a neighbour to the default transport.
func (n *Nylon) defaultLinks(id state.NodeId) []LinkConfig {
	transport := n.defaultTransport()
	if transport == nil {
		return nil
	}
	var configs []LinkConfig
	if n.IsRouter(id) {
		for _, address := range n.GetRouter(id).Endpoints {
			configs = append(configs, LinkConfig{Transport: transport, Address: address})
		}
	}
	if len(configs) == 0 {
		configs = append(configs, LinkConfig{Transport: transport})
	}
	return configs
}

func (n *Nylon) configuredLinks(id state.NodeId) []configuredLink {
	configs := n.defaultLinks(id)
	if n.linksForNeighbour != nil {
		configs = n.linksForNeighbour(id)
	}
	neigh := n.RouterState.GetNeighbour(id)
	claimed := make(map[*state.NylonEndpoint]bool)
	// boundElsewhere reports whether a health entry already belongs to another transport.
	boundElsewhere := func(health *state.NylonEndpoint, transport polyamide.Transport) bool {
		return slices.ContainsFunc(n.neighbourLinks[id], func(old neighbourLink) bool {
			return old.health == health && old.Peer.Transport() != transport
		})
	}
	var links []configuredLink
	for _, cfg := range configs {
		var health *state.NylonEndpoint
		// Only neighbours are probed, so only they get link health.
		if cfg.Address != "" && neigh != nil {
			for _, old := range n.neighbourLinks[id] {
				if old.health != nil && !claimed[old.health] && !old.health.IsRemote() && old.Peer.Transport() == cfg.Transport && old.health.Address == cfg.Address {
					health = old.health
					break
				}
			}
			if health == nil {
				// Adopt entries created by router-state reconciliation.
				for _, ep := range neigh.Eps {
					ep := ep.AsNylonEndpoint()
					if !claimed[ep] && !ep.IsRemote() && ep.Address == cfg.Address && !boundElsewhere(ep, cfg.Transport) {
						health = ep
						break
					}
				}
			}
			if health == nil {
				health = state.NewEndpoint(cfg.Address, false, &n.RouterTunables)
			}
			claimed[health] = true
		}
		links = append(links, configuredLink{LinkConfig: cfg, health: health})
	}
	// Keep learned endpoints for transports that still have a configured binding.
	for _, old := range n.neighbourLinks[id] {
		if old.health == nil || claimed[old.health] || !old.health.IsRemote() || !old.health.IsAlive() {
			continue
		}
		transport := old.Peer.Transport()
		if !slices.ContainsFunc(configs, func(c LinkConfig) bool { return c.Transport == transport }) {
			continue
		}
		claimed[old.health] = true
		links = append(links, configuredLink{LinkConfig: LinkConfig{Transport: transport, Address: old.health.Address}, health: old.health})
	}
	return links
}

// pruneLinks drops links whose health was garbage collected from router state.
func (n *Nylon) pruneLinks() {
	for _, neigh := range n.RouterState.Neighbours {
		n.neighbourLinks[neigh.Id] = slices.DeleteFunc(n.neighbourLinks[neigh.Id], func(link neighbourLink) bool {
			return link.health != nil && !slices.ContainsFunc(neigh.Eps, func(ep state.Endpoint) bool { return ep.AsNylonEndpoint() == link.health })
		})
	}
}

// linkAddress is the resolved destination of a link, or "" when it is unknown.
func (n *Nylon) linkAddress(link neighbourLink) string {
	if link.Endpoint != nil {
		return link.Endpoint.Address()
	}
	if link.health != nil {
		if address, err := n.EndpointResolver.Get(link.health.Address); err == nil {
			return address.String()
		}
	}
	return ""
}
