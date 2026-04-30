package core

import (
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
}

func (n *Nylon) Probe(node state.NodeId, ep *state.NylonEndpoint) error {
	token := rand.Uint64()
	ping := &protocol.Ny{
		Type: &protocol.Ny_ProbeOp{
			ProbeOp: &protocol.Ny_Probe{
				Token:         token,
				ResponseToken: nil,
			},
		},
	}
	peer := n.Device.LookupPeer(device.NoisePublicKey(n.GetNode(node).PubKey))
	nep, err := ep.GetWgEndpoint(n.Device)
	if err != nil {
		return err
	}
	err = n.SendNylon(ping, nep, peer)
	if err != nil {
		return err
	}

	n.PingBuf.Set(token, EpPing{
		TimeSent: time.Now(),
	}, ttlcache.DefaultTTL)
	return nil
}

func handleProbe(n *Nylon, pkt *protocol.Ny_Probe, endpoint conn.Endpoint, peer *device.Peer, node state.NodeId) {
	if pkt.ResponseToken == nil {
		// ping
		// build pong response
		res := pkt
		res.ResponseToken = new(rand.Uint64())

		// send pong
		err := n.SendNylon(&protocol.Ny{Type: &protocol.Ny_ProbeOp{ProbeOp: pkt}}, endpoint, peer)
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

func handleProbePing(n *Nylon, node state.NodeId, ep conn.Endpoint) {
	if node == n.LocalCfg.Id {
		return
	}
	// check if link exists
	for _, neigh := range n.RouterState.Neighbours {
		for _, dep := range neigh.Eps {
			dep := dep.AsNylonEndpoint()
			ap, err := dep.DynEP.Get()
			if err == nil && ap == ep.DstIPPort() && neigh.Id == node {
				// we have a link

				// refresh wireguard ep
				dep.WgEndpoint = ep

				if !dep.IsActive() {
					n.UpdateNeighbour(node)
				}
				dep.Renew()

				if state.DBG_log_probe {
					n.Log.Debug("probe from", "addr", ap.String())
				}
				return
			}
		}
	}
	// create a new link if we dont have a link
	for _, neigh := range n.RouterState.Neighbours {
		if neigh.Id == node {
			newEp := state.NewEndpoint(state.NewDynamicEndpoint(ep.DstIPPort().String()), true, ep)
			newEp.Renew()
			neigh.Eps = append(neigh.Eps, newEp)
			// push route update to improve convergence time
			n.UpdateNeighbour(node)
			return
		}
	}
}

func handleProbePong(n *Nylon, node state.NodeId, token uint64, ep conn.Endpoint) {
	// check if link exists
	for _, neigh := range n.RouterState.Neighbours {
		for _, dpLink := range neigh.Eps {
			dpLink := dpLink.AsNylonEndpoint()
			ap, err := dpLink.DynEP.Get()
			if err == nil && ap == ep.DstIPPort() && neigh.Id == node {
				linkHealth, ok := n.PingBuf.GetAndDelete(token)
				if ok {
					health := linkHealth.Value()
					latency := time.Since(health.TimeSent)
					// we have a link
					if state.DBG_log_probe {
						n.Log.Debug("probe back", "peer", node, "ping", latency)
					}
					dpLink.Renew()
					dpLink.UpdatePing(latency)

					// update wireguard endpoint
					dpLink.WgEndpoint = ep

					ComputeRoutes(n.RouterState, n)
				}
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
				go func() {
					err := n.Probe(neigh.Id, ep.AsNylonEndpoint())
					if err != nil {
						n.Log.Debug("probe failed", "err", err.Error())
					}
				}()
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
				dpl := state.NewEndpoint(ep, false, nil)
				neigh.Eps = append(neigh.Eps, dpl)
				go func() {
					err := n.Probe(peer, dpl)
					if err != nil {
						//n.Log.Debug("discovery probe failed", "err", err.Error())
					}
				}()
			}
		}
	}
	return nil
}
