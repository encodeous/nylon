package core

import (
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
)

const (
	NyProtoId = 8
)

// polyamide traffic control for nylon

func (n *Nylon) InstallTC(s *state.State) {
	r := Get[*NylonRouter](s)

	// bounce back packets if using system routing
	if n.env.UseSystemRouting {
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			if packet.Incoming() {
				// bounce incoming packets
				//dev.Log.Verbosef("BounceFwd packet: %v -> %v", packet.GetSrc(), packet.GetDst())
				return device.TcBounce, nil
			}
			return device.TcPass, nil
		})
		// forward only outgoing packets based on the routing table
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			entry, ok := r.ForwardTable.Lookup(packet.GetDst())
			if ok && !packet.Incoming() {
				packet.ToPeer = entry.Peer
				//dev.Log.Verbosef("Fwd packet: %v -> %v, via %s", packet.GetSrc(), packet.GetDst(), entry.Nh)
				return device.TcForward, nil
			}
			return device.TcPass, nil
		})
	} else {
		// forward packets based on the routing table
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			entry, ok := r.ForwardTable.Lookup(packet.GetDst())
			if ok {
				packet.ToPeer = entry.Peer
				return device.TcForward, nil
			}
			return device.TcPass, nil
		})

		// handle TTL
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			if packet.Incoming() && (packet.GetIPVersion() == 4 || packet.GetIPVersion() == 6) {
				// allow traceroute to figure out the route
				ttl := packet.GetTTL()
				if ttl >= 1 {
					ttl--
					packet.DecrementTTL()
				}
				if ttl == 0 {
					return device.TcBounce, nil
				}
			}
			return device.TcPass, nil
		})
	}

	// handle passive client traffic separately

	// bounce back packets destined for the current node
	n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
		entry, ok := r.LoopbackTable.Lookup(packet.GetDst())
		// we should only accept packets destined to us, but not our passive clients
		if ok && entry.Nh == s.Id {
			//dev.Log.Verbosef("BounceCur packet: %v -> %v", packet.GetSrc(), packet.GetDst())
			return device.TcBounce, nil
		}
		//dev.Log.Verbosef("pass packet: %v -> %v, %v", packet.GetSrc(), packet.GetDst(), entry.Nh)
		return device.TcPass, nil
	})

	// handle incoming nylon packets
	n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
		if packet.Incoming() && packet.GetIPVersion() == NyProtoId {
			n.handleNylonPacket(packet.Payload(), packet.FromEp, packet.FromPeer)
			return device.TcDrop, nil
		}
		return device.TcPass, nil
	})
}

func (n *Nylon) SendNylon(pkt *protocol.Ny, endpoint conn.Endpoint, peer *device.Peer) error {
	return n.SendNylonBundle(&protocol.TransportBundle{Packets: []*protocol.Ny{pkt}}, endpoint, peer)
}

func (n *Nylon) SendNylonBundle(pkt *protocol.TransportBundle, endpoint conn.Endpoint, peer *device.Peer) error {
	tce := n.Device.NewTCElement()
	offset := device.MessageTransportOffsetContent + device.PolyHeaderSize
	buf, err := proto.MarshalOptions{
		Deterministic: true,
	}.MarshalAppend(tce.Buffer[offset:offset], pkt)
	if err != nil {
		n.Device.PutMessageBuffer(tce.Buffer)
		n.Device.PutTCElement(tce)
		return err
	}
	tce.InitPacket(NyProtoId, uint16(len(buf)+device.PolyHeaderSize))
	tce.Priority = device.TcHighPriority

	tce.ToEp = endpoint
	tce.ToPeer = peer

	// TODO: Optimize? is it worth it?

	tcs := device.NewTCState()

	n.Device.TCBatch([]*device.TCElement{tce}, tcs)
	return nil
}

func (n *Nylon) handleNylonPacket(packet []byte, endpoint conn.Endpoint, peer *device.Peer) {
	bundle := &protocol.TransportBundle{}
	err := proto.Unmarshal(packet, bundle)
	if err != nil {
		// log skipped message
		n.env.Log.Debug("Failed to unmarshal packet", "err", err)
		return
	}

	e := n.env

	neigh := e.FindNodeBy(state.NyPublicKey(peer.GetPublicKey()))
	if neigh == nil {
		// this should not be possible
		panic("impossible state, peer added, but not a node in the network")
		return
	}

	defer func() {
		err := recover()
		if err != nil {
			n.env.Log.Error("panic while handling poly socket: %v", err)
		}
	}()

	for _, pkt := range bundle.Packets {
		switch pkt.Type.(type) {
		case *protocol.Ny_SeqnoRequestOp:
			e.Dispatch(func(s *state.State) error {
				return routerHandleSeqnoRequest(s, *neigh, pkt.GetSeqnoRequestOp())
			})
		case *protocol.Ny_RouteOp:
			e.Dispatch(func(s *state.State) error {
				return routerHandleRouteUpdate(s, *neigh, pkt.GetRouteOp())
			})
		case *protocol.Ny_AckRetractOp:
			e.Dispatch(func(s *state.State) error {
				return routerHandleAckRetract(s, *neigh, pkt.GetAckRetractOp())
			})
		case *protocol.Ny_ProbeOp:
			handleProbe(n, pkt.GetProbeOp(), endpoint, peer, *neigh)
		}
	}
}
