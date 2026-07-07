package core

import (
	"cmp"
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
)

type EpPing struct {
	TimeSent time.Time
	Peer     state.NodeId
	Complete func(protocol.EndpointProbeStatus, time.Duration)
}

func (n *Nylon) sendEndpointProbes(peer state.NodeId, timeout time.Duration) ([]Future[*protocol.EndpointProbeResult], error) {
	neigh := n.RouterState.GetNeighbour(peer)
	if neigh == nil {
		return nil, fmt.Errorf("peer %q is not a neighbour", peer)
	}
	eps := slices.Clone(neigh.Eps)
	slices.SortFunc(eps, func(a, b state.Endpoint) int {
		return cmp.Compare(a.AsNylonEndpoint().DynEP.Value, b.AsNylonEndpoint().DynEP.Value)
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
	address := ep.DynEP.Value
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

	peer := n.Device.LookupPeer(device.NoisePublicKey(n.GetNode(node).PubKey))
	if peer == nil {
		return fail(protocol.EndpointProbeStatus_ENDPOINT_PROBE_SEND_ERROR, fmt.Errorf("wireguard peer %q is not configured", node))
	}
	nep, err := ep.GetWgEndpoint(n.Device)
	if err != nil {
		return fail(protocol.EndpointProbeStatus_ENDPOINT_PROBE_RESOLVE_ERROR, err)
	}
	resolved = nep.DstIPPort().String()
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
		TimeSent: sentAt,
		Peer:     node,
		Complete: func(status protocol.EndpointProbeStatus, latency time.Duration) {
			if timeoutTimer != nil {
				timeoutTimer.Stop()
			}
			completeEndpoint(status, latency, nil)
		},
	}, ttlcache.DefaultTTL)

	go func() {
		err := n.SendNylon(ping, nep, peer)
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

func handleProbe(n *Nylon, pkt *protocol.Ny_Probe, endpoint conn.Endpoint, peer *device.Peer, node state.NodeId) {
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
			handleProbePing(n, node, endpoint)
			return nil
		})
	} else {
		// pong
		n.Dispatch(func() error {
			handleProbePong(n, node, pkt.Token, endpoint)
			return nil
		})
	}
}

func handleProbePing(n *Nylon, node state.NodeId, wgEndpoint conn.Endpoint) {
	if node == n.LocalCfg.Id {
		return
	}
	// check if link exists
	for _, neigh := range n.RouterState.Neighbours {
		for _, dep := range neigh.Eps {
			dep := dep.AsNylonEndpoint()
			ap, err := dep.DynEP.Get()
			if err == nil && ap == wgEndpoint.DstIPPort() && neigh.Id == node {
				// we have a link

				// refresh wireguard ep
				dep.WgEndpoint = wgEndpoint

				wasInactive := !dep.IsActive()
				dep.Renew()
				if wasInactive {
					ComputeRoutes(n.RouterState, n)
					n.UpdateNeighbour(node)
				}

				if n.DBG_log_probe {
					n.Log.Debug("probe from", "addr", ap.String())
				}
				return
			}
		}
	}
	// create a new link if we dont have a link
	for _, neigh := range n.RouterState.Neighbours {
		if neigh.Id == node {
			newEp := state.NewEndpoint(state.NewDynamicEndpoint(wgEndpoint.DstIPPort().String()), true, wgEndpoint, &n.RouterTunables)
			newEp.Renew()
			neigh.Eps = append(neigh.Eps, newEp)
			// push route update to improve convergence time
			ComputeRoutes(n.RouterState, n)
			n.UpdateNeighbour(node)
			return
		}
	}
}

func handleProbePong(n *Nylon, node state.NodeId, token uint64, ep conn.Endpoint) {
	linkHealth, ok := n.PingBuf.GetAndDelete(token)
	if !ok {
		return
	}
	health := linkHealth.Value()
	if health.Peer != "" && health.Peer != node {
		n.Log.Warn("probe came back from wrong peer", "expected", health.Peer, "actual", node)
		return
	}
	receivedAt := time.Now()
	latency := receivedAt.Sub(health.TimeSent)
	health.Complete(protocol.EndpointProbeStatus_ENDPOINT_PROBE_REPLIED, latency)

	// check if link exists
	for _, neigh := range n.RouterState.Neighbours {
		for _, dep := range neigh.Eps {
			dpLink := dep.AsNylonEndpoint()
			ap, err := dpLink.DynEP.Get()
			if err == nil && ap == ep.DstIPPort() && neigh.Id == node {
				// we have a link
				if n.DBG_log_probe {
					n.Log.Debug("probe back", "peer", node, "ping", latency)
				}
				dpLink.Renew()
				dpLink.UpdatePing(latency)

				// update wireguard endpoint
				dpLink.WgEndpoint = ep

				ComputeRoutes(n.RouterState, n)
				return
			}
		}
	}
	n.Log.Warn("probe came back and couldn't find link", "from", ep.DstToString(), "node", node)
}

func (n *Nylon) probeLinks(active bool) error {
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
	// probe for new dp links
	for _, peer := range n.GetPeers(n.LocalCfg.Id) {
		if !n.IsRouter(peer) {
			continue
		}
		neigh := n.RouterState.GetNeighbour(peer)
		if neigh == nil {
			continue
		}
		cfg := n.GetRouter(peer)
		// assumption: we don't need to connect to the same endpoint again within the scope of the same node
		for _, ep := range cfg.Endpoints {
			ap, err := ep.Get()
			if err != nil {
				continue
			}
			idx := slices.IndexFunc(neigh.Eps, func(link state.Endpoint) bool {
				lap, err := link.AsNylonEndpoint().DynEP.Get()
				if err != nil {
					return false
				}
				return !link.IsRemote() && lap == ap
			})
			if idx == -1 {
				// add the link to the neighbour
				dpl := state.NewEndpoint(ep, false, nil, &n.RouterTunables)
				neigh.Eps = append(neigh.Eps, dpl)
				err := n.Probe(peer, dpl)
				if err != nil {
					//n.Log.Debug("discovery probe failed", "err", err.Error())
				}
			}
		}
	}
	return nil
}
