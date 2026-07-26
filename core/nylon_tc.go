package core

import (
	"fmt"
	"net/netip"

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

func (n *Nylon) InstallTC() {
	t := n.Trace

	// exit-encap filter: outbound IP packets that fall off our routing
	// table get wrapped in a NyUnicast / exit packet bound for the
	// configured exit node. Installed before the generic forwarder so
	// that locally-originated traffic to "the internet" gets captured
	// here first. Reads only the atomic ExitFilter snapshot, never the
	// live LocalCfg or CentralCfg.
	n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
		snap := n.ExitFilter.Load()
		if snap == nil || snap.ExitNodeBin == state.InvalidNodeIdBin {
			return device.TcPass, nil
		}
		if packet.Incoming() || !packet.Validate() {
			return device.TcPass, nil
		}
		ver := packet.GetIPVersion()
		if ver != 4 && ver != 6 {
			return device.TcPass, nil
		}
		dst := packet.GetDst()
		if _, ok := n.router.ForwardTable.Load().Lookup(dst); ok {
			return device.TcPass, nil // overlay route exists; keep normal routing
		}
		if state.IsDefaultLocalExcludedAddr(dst) {
			return device.TcDrop, nil
		}
		entry, ok := snap.NodeForward[snap.ExitNodeBin]
		if !ok || entry.Peer == nil {
			if n.DBG_trace_tc {
				t.Submit(fmt.Sprintf("ExitDrop: %v -> %v, exit %s, reason no_route\n", packet.GetSrc(), dst, snap.ExitNode))
			}
			return device.TcDrop, nil
		}
		src := packet.GetSrc()
		if err := wrapExitPacket(packet, snap.ExitNodeBin, snap.LocalIdBin); err != nil {
			if n.DBG_trace_tc {
				t.Submit(fmt.Sprintf("ExitDrop: %v -> %v, exit %s, reason %v\n", src, dst, snap.ExitNode, err))
			}
			return device.TcDrop, nil
		}
		packet.ToPeer = entry.Peer
		packet.Priority = device.TcMediumPriority
		if n.DBG_trace_tc {
			t.Submit(fmt.Sprintf("ExitEncap: %v -> %v, exit %s via %s\n", src, dst, snap.ExitNode, entry.Nh))
		}
		return device.TcForward, nil
	})

	if n.DBG_trace_tc {
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			if packet.Validate() { // make sure it's an IP packet
				peer := packet.FromPeer
				if peer == nil {
					peer = packet.ToPeer
				}
				src := packet.GetSrc()
				dst := packet.GetDst()
				if src.IsValid() &&
					dst.IsValid() &&
					peer != nil &&
					src != netip.IPv4Unspecified() && src != netip.IPv6Unspecified() &&
					dst != netip.IPv4Unspecified() && dst != netip.IPv6Unspecified() {
					t.Submit(fmt.Sprintf("Unhandled TC packet: %v -> %v, peer %s\n", packet.GetSrc(), packet.GetDst(), peer))
				}
			}
			return device.TcPass, nil
		})
	}

	// bounce back packets if using system routing
	if n.UseSystemRouting {
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
			entry, ok := n.router.ForwardTable.Load().Lookup(packet.GetDst())
			if ok && !packet.Incoming() {
				if entry.Blackhole {
					return device.TcDrop, nil
				}
				packet.ToPeer = entry.Peer
				if n.DBG_trace_tc {
					t.Submit(fmt.Sprintf("Fwd packet: %v -> %v, via %s\n", packet.GetSrc(), packet.GetDst(), entry.Nh))
				}
				return device.TcForward, nil
			}
			return device.TcPass, nil
		})
	} else {
		// forward packets based on the routing table
		n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
			entry, ok := n.router.ForwardTable.Load().Lookup(packet.GetDst())
			if ok {
				if entry.Blackhole {
					return device.TcDrop, nil
				}
				packet.ToPeer = entry.Peer
				if n.DBG_trace_tc {
					t.Submit(fmt.Sprintf("Fwd packet: %v -> %v, via %s\n", packet.GetSrc(), packet.GetDst(), entry.Nh))
				}
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
					if n.DBG_trace_tc {
						t.Submit(fmt.Sprintf("TTL Expired: %v -> %v\n", packet.GetSrc(), packet.GetDst()))
					}
					return device.TcBounce, nil
				}
			}
			return device.TcPass, nil
		})
	}

	// handle passive client traffic separately

	// bounce back packets destined for the current node
	n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
		entry, ok := n.router.ExitTable.Load().Lookup(packet.GetDst())
		// we should only accept packets destined to us, but not our passive clients
		if ok && entry.Nh == n.LocalCfg.Id {
			if n.DBG_trace_tc {
				t.Submit(fmt.Sprintf("Exit: %v -> %v\n", packet.GetSrc(), packet.GetDst()))
			}
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

	// handle incoming NyUnicast packets. Installed last so that under
	// reverse-installation evaluation it runs first; this ensures
	// inbound exit-transit packets get re-forwarded before any of the
	// IP-routing filters above ever see them.
	n.Device.InstallFilter(func(dev *device.Device, packet *device.TCElement) (device.TCAction, error) {
		if !packet.Incoming() || packet.GetIPVersion() != NyUnicastProtoId {
			return device.TcPass, nil
		}
		snap := n.ExitFilter.Load()
		if snap == nil {
			return device.TcDrop, nil
		}
		payload := packet.Payload()
		h, err := parseNyUnicastHeader(payload)
		if err != nil {
			if n.DBG_trace_tc {
				t.Submit(fmt.Sprintf("ExitDrop: malformed header: %v\n", err))
			}
			return device.TcDrop, nil
		}
		switch h.subtype {
		case NyUnicastSubtypeExit:
			action, err := n.handleExitPacket(packet, snap, h)
			if err != nil && n.DBG_trace_tc {
				t.Submit(fmt.Sprintf("ExitDrop: reason %v\n", err))
			}
			return action, err
		default:
			if n.DBG_trace_tc {
				t.Submit(fmt.Sprintf("ExitDrop: unknown unicast subtype %d\n", h.subtype))
			}
			return device.TcDrop, nil
		}
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
	// we need to be careful here, since this function is called on the dataplane
	bundle := &protocol.TransportBundle{}
	err := proto.Unmarshal(packet, bundle)
	if err != nil {
		// log skipped message
		n.Log.Debug("Failed to unmarshal packet", "err", err)
		return
	}

	nt := n.PeerMap.Load()
	if nt == nil {
		return // not loaded yet
	}
	neigh, ok := (*nt)[state.NyPublicKey(peer.GetPublicKey())]
	if !ok {
		// this should not be possible
		panic("impossible state, peer added, but not a node in the network")
	}

	defer func() {
		err := recover()
		if err != nil {
			n.Log.Error("panic while handling poly socket", "err", err)
		}
	}()

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
