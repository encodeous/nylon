package core

import (
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
)

const (
	NyProtoId  = 8
	NyPriority = 10
)

// polyamide traffic control for nylon

func (n *Nylon) InstallTC() {
	// bounce back packets if using system routing
	if n.env.UseSystemRouting {
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			if packet.Incoming() {
				// bounce incoming packets
				return device.TcBounce, nil
			}
			return device.TcPass, nil
		})
	}

	// bounce back packets destined for the current node
	n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
		if n.env.GetNode(n.env.Id).Address == packet.GetDst() {
			return device.TcBounce, nil
		}
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

func (n *Nylon) SendNylon(pkt proto.Message, endpoint conn.Endpoint, peer *device.Peer) error {
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

	tcc := n.Device.GetTCElementsContainer()
	tcc.Elems = append(tcc.Elems, tce)
	n.Device.EnqueueTC(tcc)
	return nil
}

func (n *Nylon) handleNylonPacket(packet []byte, endpoint conn.Endpoint, peer *device.Peer) {
	pkt := &protocol.Ny{}
	err := proto.Unmarshal(packet, pkt)
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

	switch pkt.Type.(type) {
	case *protocol.Ny_SeqnoRequestOp:
		e.Dispatch(func(s *state.State) error {
			return routerHandleSeqnoRequest(s, *neigh, pkt.GetSeqnoRequestOp())
		})
	case *protocol.Ny_RouteOp:
		e.Dispatch(func(s *state.State) error {
			return routerHandleRouteUpdate(s, *neigh, pkt.GetRouteOp())
		})
	case *protocol.Ny_ProbeOp:
		handleProbe(n, pkt.GetProbeOp(), endpoint, peer, *neigh)
	}
	defer func() {
		err := recover()
		if err != nil {
			n.env.Log.Error("panic while handling poly socket: %v", err)
		}
	}()
}
