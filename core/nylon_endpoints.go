package core

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
)

type EpPing struct {
	TimeSent  time.Time
	Peer      state.NodeId
	Transport polyamide.Transport
	Complete  func(protocol.EndpointProbeStatus, time.Duration)
}

func (n *Nylon) sendEndpointProbes(peer state.NodeId, timeout time.Duration) ([]Future[*protocol.EndpointProbeResult], error) {
	neigh := n.RouterState.GetNeighbour(peer)
	if neigh == nil {
		return nil, fmt.Errorf("peer %q is not a neighbour", peer)
	}
	eps := slices.Clone(neigh.Eps)
	slices.SortFunc(eps, func(a, b state.Endpoint) int {
		return cmp.Compare(a.AsNylonEndpoint().Address, b.AsNylonEndpoint().Address)
	})
	probes := make([]Future[*protocol.EndpointProbeResult], 0, len(eps))
	for _, ep := range eps {
		result, _ := n.sendEndpointProbe(neigh.Id, ep.AsNylonEndpoint(), timeout)
		probes = append(probes, result)
	}
	return probes, nil
}

func (n *Nylon) Probe(node state.NodeId, ep *state.NylonEndpoint) error {
	_, err := n.sendEndpointProbe(node, ep, 0)
	return err
}

func (n *Nylon) sendEndpointProbe(node state.NodeId, ep *state.NylonEndpoint, timeout time.Duration) (Future[*protocol.EndpointProbeResult], error) {
	address := ep.Address
	resolved := ""
	resultFuture, completeResult := NewFuture[*protocol.EndpointProbeResult]()
	completeEndpoint := func(status protocol.EndpointProbeStatus, latency time.Duration, err error) {
		result := &protocol.EndpointProbeResult{
			Address:   address,
			Status:    status,
			LatencyNs: int64(latency),
		}
		if resolved != "" {
			result.Resolved = new(resolved)
		}
		completeResult(result, err)
	}
	fail := func(status protocol.EndpointProbeStatus, err error) (Future[*protocol.EndpointProbeResult], error) {
		completeEndpoint(status, 0, err)
		return resultFuture, err
	}

	index := slices.IndexFunc(n.neighbourLinks[node], func(link neighbourLink) bool { return link.health == ep })
	if index == -1 {
		return fail(protocol.EndpointProbeStatus_ENDPOINT_PROBE_SEND_ERROR, fmt.Errorf("endpoint for neighbour %q is not configured", node))
	}
	// Refresh the native endpoint when DNS changes the destination.
	link := n.neighbourLinks[node][index].Link
	current, err := n.EndpointResolver.Get(ep.Address)
	if err != nil {
		return fail(protocol.EndpointProbeStatus_ENDPOINT_PROBE_RESOLVE_ERROR, err)
	}
	if link.Endpoint == nil || link.Endpoint.Address() != current.String() {
		endpoint, err := link.Peer.Transport().PrepareEndpoint(n.Context, current.String())
		if err != nil {
			return fail(protocol.EndpointProbeStatus_ENDPOINT_PROBE_RESOLVE_ERROR, err)
		}
		link.Endpoint = endpoint
		n.neighbourLinks[node][index].Endpoint = endpoint
	}
	resolved = link.Endpoint.Address()
	token := rand.Uint64()
	sentAt := time.Now()
	ping := &protocol.Ny{
		Type: &protocol.Ny_ProbeOp{
			ProbeOp: &protocol.Ny_Probe{
				Token:         token,
				ResponseToken: nil,
			},
		},
	}
	var timeoutTimer *time.Timer
	if timeout > 0 {
		timeoutTimer = time.AfterFunc(timeout, func() {
			n.PingBuf.Delete(token)
			completeEndpoint(protocol.EndpointProbeStatus_ENDPOINT_PROBE_TIMEOUT, 0, nil)
		})
	}

	n.PingBuf.Set(token, EpPing{
		TimeSent:  sentAt,
		Peer:      node,
		Transport: link.Peer.Transport(),
		Complete: func(status protocol.EndpointProbeStatus, latency time.Duration) {
			if timeoutTimer != nil {
				timeoutTimer.Stop()
			}
			completeEndpoint(status, latency, nil)
		},
	}, ttlcache.DefaultTTL)

	go func() {
		err := n.SendNylon(ping, link.Endpoint, link.Peer)
		if err != nil {
			if timeoutTimer != nil {
				timeoutTimer.Stop()
			}
			n.PingBuf.Delete(token)
			completeEndpoint(protocol.EndpointProbeStatus_ENDPOINT_PROBE_SEND_ERROR, 0, err)
		}
	}()

	return resultFuture, nil
}

func handleProbe(n *Nylon, pkt *protocol.Ny_Probe, endpoint polyamide.Endpoint, peer polyamide.Peer, node state.NodeId) {
	if pkt.ResponseToken == nil {
		// ping
		// build pong response
		responseToken := pkt.Token
		res := &protocol.Ny_Probe{
			Token:         pkt.Token,
			ResponseToken: &responseToken,
		}

		// send pong
		err := n.SendNylon(&protocol.Ny{Type: &protocol.Ny_ProbeOp{ProbeOp: res}}, endpoint, peer)
		if err != nil {
			n.Log.Error("Failed to send nylon packet to node", "node", node, "error", err)
			return
		}

		n.Dispatch(func() error {
			handleProbePing(n, node, endpoint, peer)
			return nil
		})
	} else {
		// pong
		n.Dispatch(func() error {
			handleProbePong(n, node, pkt.Token, endpoint, peer)
			return nil
		})
	}
}

func (n *Nylon) receivedLink(node state.NodeId, endpoint polyamide.Endpoint, peer polyamide.Peer) *state.NylonEndpoint {
	if endpoint == nil || peer == nil || n.transportPeer(node, peer.Transport()) != peer {
		return nil
	}
	for i, link := range n.neighbourLinks[node] {
		if link.health != nil && link.Peer == peer && n.linkAddress(link) == endpoint.Address() {
			n.neighbourLinks[node][i].Endpoint = endpoint
			return link.health
		}
	}
	neigh := n.RouterState.GetNeighbour(node)
	if neigh == nil {
		return nil
	}
	health := state.NewEndpoint(endpoint.Address(), true, &n.RouterTunables)
	neigh.Eps = append(neigh.Eps, health)
	if n.neighbourLinks == nil {
		n.neighbourLinks = make(map[state.NodeId][]neighbourLink)
	}
	n.neighbourLinks[node] = append(n.neighbourLinks[node], neighbourLink{Link: Link{Peer: peer, Endpoint: endpoint}, health: health})
	return health
}

func handleProbePing(n *Nylon, node state.NodeId, endpoint polyamide.Endpoint, peer polyamide.Peer) {
	n.renewLink(node, endpoint, peer)
}

// endpointLearned handles a packet from peer at an endpoint its transport was not
// configured with, such as after the peer roams.
func (n *Nylon) endpointLearned(node state.NodeId, endpoint polyamide.Endpoint, peer polyamide.Peer) {
	if n.transportPeer(node, peer.Transport()) != peer {
		return // the handle was retired
	}
	if n.IsClient(node) {
		n.passiveClientRoamed(peer)
		return
	}
	n.renewLink(node, endpoint, peer)
}

// renewLink records traffic from a neighbour at endpoint, learning it as a link if needed.
func (n *Nylon) renewLink(node state.NodeId, endpoint polyamide.Endpoint, peer polyamide.Peer) {
	if node == n.LocalCfg.Id {
		return
	}
	health := n.receivedLink(node, endpoint, peer)
	if health == nil {
		return
	}
	wasInactive := !health.IsActive()
	health.Renew()
	if wasInactive {
		ComputeRoutes(n.RouterState, n)
		n.UpdateNeighbour(node)
	}
	n.publishLinks()
}

func handleProbePong(n *Nylon, node state.NodeId, token uint64, ep polyamide.Endpoint, peer polyamide.Peer) {
	linkHealth, ok := n.PingBuf.GetAndDelete(token)
	if !ok {
		return
	}
	health := linkHealth.Value()
	if health.Peer != node || health.Transport != peer.Transport() {
		n.Log.Warn("probe came back from wrong peer", "expected", health.Peer, "actual", node)
		return
	}
	latency := time.Since(health.TimeSent)
	health.Complete(protocol.EndpointProbeStatus_ENDPOINT_PROBE_REPLIED, latency)
	link := n.receivedLink(node, ep, peer)
	if link == nil {
		return
	}
	link.Renew()
	link.UpdatePing(latency)
	ComputeRoutes(n.RouterState, n)
	n.publishLinks()
}

func (n *Nylon) probeLinks(active bool) error {
	defer n.publishLinks()
	// probe links
	for _, neigh := range n.RouterState.Neighbours {
		for _, ep := range neigh.Eps {
			if ep.IsActive() == active {
				err := n.Probe(neigh.Id, ep.AsNylonEndpoint())
				if err != nil {
					n.Log.Debug("probe failed", "err", err.Error())
				}
			}
		}
	}
	return nil
}

func (n *Nylon) probeNew() error {
	// probe configured links for new dp links
	for id, links := range n.neighbourLinks {
		for _, link := range links {
			if link.health == nil || link.health.IsRemote() {
				continue
			}
			if _, err := n.EndpointResolver.Get(link.health.Address); err != nil {
				continue
			}
			_ = n.Probe(id, link.health)
		}
	}
	return nil
}
