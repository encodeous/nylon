package core

import (
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
)

func (n *Nylon) Receive(packet []byte, endpoint conn.Endpoint, peer *device.Peer) {
	pkt := &protocol.Ny{}
	err := proto.Unmarshal(packet, pkt)
	if err != nil {
		// log skipped message
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
		HandleProbe(e, n.PolySock, pkt.GetProbeOp(), endpoint, peer, *neigh)
	}
	defer func() {
		err := recover()
		if err != nil {
			n.env.Log.Error("panic while handling poly socket: %v", err)
		}
	}()
}
