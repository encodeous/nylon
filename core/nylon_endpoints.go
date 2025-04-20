package core

import (
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
	"google.golang.org/protobuf/proto"
	"math/rand/v2"
	"slices"
	"time"
)

type EpPing struct {
	TimeSent time.Time
}

func (n *Nylon) Probe(e *state.Env, ep *state.DynamicEndpoint) error {
	token := rand.Uint64()
	ping := &protocol.Ny{
		Type: &protocol.Ny_ProbeOp{
			ProbeOp: &protocol.Ny_Probe{
				Token:         token,
				ResponseToken: nil,
			},
		},
	}
	marshal, err := proto.Marshal(ping)
	if err != nil {
		return err
	}

	n.Send(marshal, ep)
	n.PingBuf.Set(token, EpPing{
		TimeSent: time.Now(),
	}, ttlcache.DefaultTTL)
	return nil
}

func (n *Nylon) Send(packet []byte, ep *state.DynamicEndpoint) {
	neigh := n.env.GetRouter(ep.Node())
	dev := n.Device
	n.PolySock.Send(packet, ep.NetworkEndpoint().GetWgEndpoint(dev), dev.LookupPeer(device.NoisePublicKey(neigh.PubKey)))
}

func HandleProbe(e *state.Env, sock *device.PolySock, pkt *protocol.Ny_Probe, endpoint conn.Endpoint, peer *device.Peer, node state.NodeId) {
	if pkt.ResponseToken == nil {
		// ping
		// build pong response
		res := pkt
		token := rand.Uint64()
		res.ResponseToken = &token

		// send pong
		pktBytes, err := proto.Marshal(&protocol.Ny{Type: &protocol.Ny_ProbeOp{ProbeOp: pkt}})
		if err != nil {
			return
		}
		sock.Send(pktBytes, endpoint, peer)

		e.Dispatch(func(s *state.State) error {
			return handleProbePing(s, node, endpoint)
		})
	} else {
		// pong
		e.Dispatch(func(s *state.State) error {
			handleProbePong(s, node, pkt.Token, endpoint)
			return nil
		})
	}
}

func handleProbePing(s *state.State, node state.NodeId, ep conn.Endpoint) error {
	if node == s.Id {
		return nil
	}
	// check if link exists
	for _, neigh := range s.Neighbours {
		for _, dep := range neigh.Eps {
			if dep.NetworkEndpoint().Ep == ep.DstIPPort() && neigh.Id == node {
				// we have a link

				// refresh wireguard ep
				dep.NetworkEndpoint().WgEndpoint = ep

				if !dep.IsActive() {
					err := pushRouteTable(s, &node)
					if err != nil {
						return err
					}
				}
				dep.Renew()

				if state.DBG_log_probe {
					s.Log.Debug("probe from", "addr", dep.NetworkEndpoint().Ep)
				}
				return nil
			}
		}
	}
	// create a new link if we dont have a link
	for _, neigh := range s.Neighbours {
		if neigh.Id == node {
			neigh.Eps = append(neigh.Eps, state.NewEndpoint(ep.DstIPPort(), neigh.Id, true, ep))
			// push route update to improve convergence time
			return pushRouteTable(s, &node)
		}
	}
	return nil
}

func handleProbePong(s *state.State, node state.NodeId, token uint64, ep conn.Endpoint) {
	n := Get[*Nylon](s)
	// check if link exists
	for _, neigh := range s.Neighbours {
		for _, dpLink := range neigh.Eps {
			if dpLink.NetworkEndpoint().Ep == ep.DstIPPort() && neigh.Id == node {
				linkHealth, ok := n.PingBuf.GetAndDelete(token)
				if ok {
					health := linkHealth.Value()
					latency := time.Now().Sub(health.TimeSent)
					// we have a link
					if state.DBG_log_probe {
						s.Log.Debug("probe back", "peer", node, "ping", latency)
					}
					err := updateRoutes(s)
					if err != nil {
						s.Log.Error("Error updating routes: ", "err", err)
					}
					dpLink.UpdatePing(latency)
					dpLink.Renew()

					// update wireguard endpoint
					dpLink.NetworkEndpoint().WgEndpoint = ep
				}
				return
			}
		}
	}
	s.Log.Warn("probe came back and couldn't find link", "from", ep.DstToString(), "node", node)
	return
}

func (n *Nylon) probeLinks(s *state.State, active bool) error {
	// probe links
	for _, neigh := range s.Neighbours {
		for _, dpLink := range neigh.Eps {
			if dpLink.IsActive() == active {
				go func() {
					err := n.Probe(s.Env, dpLink)
					if err != nil {
						s.Log.Debug("probe failed", "err", err.Error())
					}
				}()
			}
		}
	}
	return nil
}

func (n *Nylon) probeNew(s *state.State) error {
	// probe for new dp links
	for _, peer := range s.GetPeers() {
		if !s.IsRouter(peer) {
			continue
		}
		neigh := s.GetNeighbour(peer)
		if neigh == nil {
			continue
		}
		cfg := s.GetRouter(peer)
		// assumption: we don't need to connect to the same endpoint again within the scope of the same node
		for _, ep := range cfg.Endpoints {
			if !ep.IsValid() {
				continue
			}
			idx := slices.IndexFunc(neigh.Eps, func(link *state.DynamicEndpoint) bool {
				return !link.IsRemote() && link.NetworkEndpoint().Ep == ep
			})
			if idx == -1 {
				// add the link to the neighbour
				dpl := state.NewEndpoint(ep, peer, false, nil)
				neigh.Eps = append(neigh.Eps, dpl)
				go func() {
					err := n.Probe(s.Env, dpl)
					if err != nil {
						//s.Log.Debug("discovery probe failed", "err", err.Error())
					}
				}()
			}
		}
	}
	return nil
}
