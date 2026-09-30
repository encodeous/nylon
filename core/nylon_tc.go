package core

import (
	"errors"
	"fmt"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
)

// routeBatch is the traffic-control filter shared by every transport and the TUN.
func (n *Nylon) routeBatch(packets []polyamide.TCElement, decisions []polyamide.TCDecision) {
	for i := range packets {
		decisions[i] = n.routePacket(packets[i])
	}
}

func (n *Nylon) transportHooks() polyamide.Hooks {
	return polyamide.Hooks{
		RouteBatch:  n.routeBatch,
		Control:     func(m polyamide.ControlMessage) { n.handleNylonPacket(m.Payload, m.Endpoint, m.From) },
		DeliverHost: n.deliverTUN,
		EndpointLearned: func(peer polyamide.Peer, endpoint polyamide.Endpoint) {
			n.Dispatch(func() error {
				n.endpointLearned(state.NodeId(peer.ID()), endpoint, peer)
				return nil
			})
		},
	}
}

// routePacket drops packets that have no route.
func (n *Nylon) routePacket(packet polyamide.TCElement) polyamide.TCDecision {
	tables := n.router.Tables.Load()
	if tables == nil {
		return polyamide.TCDecision{}
	}
	if entry, ok := tables.Exit.Lookup(packet.Destination()); ok && entry.Nh == n.LocalCfg.Id {
		if n.DBG_trace_tc {
			n.Trace.Submit(fmt.Sprintf("Exit: %v -> %v\n", packet.Source(), packet.Destination()))
		}
		return polyamide.TCDecision{Action: polyamide.TcBounce}
	}
	if packet.Incoming() {
		// The host routing table forwards incoming packets.
		if n.UseSystemRouting {
			return polyamide.TCDecision{Action: polyamide.TcBounce}
		}
		packet.DecrementTTL()
		if packet.TTL() == 0 {
			if n.DBG_trace_tc {
				n.Trace.Submit(fmt.Sprintf("TTL Expired: %v -> %v\n", packet.Source(), packet.Destination()))
			}
			return polyamide.TCDecision{Action: polyamide.TcBounce}
		}
	}
	if entry, ok := tables.Forward.Lookup(packet.Destination()); ok {
		if entry.Blackhole {
			return polyamide.TCDecision{}
		}
		if n.DBG_trace_tc {
			n.Trace.Submit(fmt.Sprintf("Fwd packet: %v -> %v, via %s\n", packet.Source(), packet.Destination(), entry.Nh))
		}
		// The transport sends to the peer's default endpoint, which SyncTransport keeps best first.
		if links := tables.Links[entry.Nh]; len(links) != 0 {
			return polyamide.TCDecision{Action: polyamide.TcForward, To: links[0].Peer}
		}
		return polyamide.TCDecision{}
	}
	if n.DBG_trace_tc {
		n.Trace.Submit(fmt.Sprintf("Unhandled TC packet: %v -> %v\n", packet.Source(), packet.Destination()))
	}
	return polyamide.TCDecision{}
}
func (n *Nylon) SendNylon(pkt *protocol.Ny, endpoint polyamide.Endpoint, peer polyamide.Peer) error {
	return n.SendNylonBundle(&protocol.TransportBundle{Packets: []*protocol.Ny{pkt}}, endpoint, peer)
}
func (n *Nylon) SendNylonBundle(pkt *protocol.TransportBundle, endpoint polyamide.Endpoint, peer polyamide.Peer) error {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(pkt)
	if err != nil {
		return err
	}
	if peer == nil || peer.Transport() == nil {
		return polyamide.ErrInvalidHandle
	}
	return peer.Transport().SendControl(n.Context, peer, endpoint, data)
}

func (n *Nylon) handleNylonPacket(packet []byte, endpoint polyamide.Endpoint, peer polyamide.Peer) {
	defer func() {
		err := recover()
		if err != nil {
			n.Log.Error("panic while handling poly socket", "err", err)
		}
	}()

	bundle := &protocol.TransportBundle{}
	err := proto.Unmarshal(packet, bundle)
	if err != nil {
		n.Log.Debug("Failed to unmarshal packet", "err", err)
		return
	}

	nt := n.PeerMap.Load()
	if nt == nil {
		return // not loaded yet
	}
	if peer == nil {
		n.Log.Debug("dropping nylon packet without a peer")
		return
	}
	key, valid := peerPublicKey(peer)
	neigh, ok := (*nt)[key]
	ok = ok && valid
	if !ok {
		// Retired generations can still deliver callbacks during reconciliation.
		n.Log.Debug("dropping nylon packet from an unknown peer", "peer", peer.ID())
		return
	}

	for _, pkt := range bundle.Packets {
		switch pkt.Type.(type) {
		case *protocol.Ny_SeqnoRequestOp:
			n.Dispatch(func() error {
				return n.routerHandleSeqnoRequest(neigh, pkt.GetSeqnoRequestOp())
			})
		case *protocol.Ny_RouteOp:
			n.Dispatch(func() error {
				return n.routerHandleRouteUpdate(neigh, pkt.GetRouteOp())
			})
		case *protocol.Ny_AckRetractOp:
			n.Dispatch(func() error {
				return n.routerHandleAckRetract(neigh, pkt.GetAckRetractOp())
			})
		case *protocol.Ny_ProbeOp:
			// we don't want to wait for dispatch before responding to this packet
			handleProbe(n, pkt.GetProbeOp(), endpoint, peer, neigh)
		}
	}
}

func (n *Nylon) routeTUNPackets(packets [][]byte) error {
	elements := make([]polyamide.TCElement, 0, len(packets))
	for _, bytes := range packets {
		data, valid := polyamide.IPBytes(bytes)
		if valid {
			elements = append(elements, polyamide.TCElement{Bytes: data})
		}
	}
	decisions := make([]polyamide.TCDecision, len(elements))
	n.routeBatch(elements, decisions)
	err := polyamide.DispatchBatch(n.Context, elements, decisions)
	var host [][]byte
	for i, decision := range decisions {
		if decision.Action == polyamide.TcBounce {
			host = append(host, elements[i].Bytes)
		}
	}
	if len(host) != 0 {
		err = errors.Join(err, n.deliverTUN(host))
	}
	return err
}
