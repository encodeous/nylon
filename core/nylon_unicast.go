package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/state"
)

// NyUnicast is a generic node-to-node tunnel packet type carried inside the
// polyamide TC layer. It supersedes the earlier "NyExit" packet by carving
// out a subtype byte so the same encapsulation can be reused for future
// node-targeted features (config push, state query, etc.) without
// re-inventing the wire format each time.
//
// Wire format (all integers big-endian):
//
//	+---------------------+-----------------------+
//	| poly outer header   | NyUnicast payload     |
//	| (PolyHeaderSize=3)  | (NyUnicastHeaderSize) |
//	+---------------------+-----------------------+
//	         3 B                       6 B
//
// Poly outer header (managed by polyamide):
//
//	byte 0   : NyUnicastProtoId << 4 (so the IP-version nibble reads as 9)
//	bytes 1-2: payload length (total packet length minus PolyHeaderSize)
//
// NyUnicast payload header (this package):
//
//	byte 0   : subtype          (see NyUnicastSubtype*)
//	byte 1   : hop_limit        (decremented at every transit node; 0 = drop)
//	bytes 2-3: dst NodeIdBin    (final destination node, big-endian uint16)
//	bytes 4-5: src NodeIdBin    (originating node, big-endian uint16)
//	bytes 6+ : subtype-specific payload
//
// For NyUnicastSubtypeExit the subtype payload is the original IPv4/IPv6
// packet emitted by the origin's TUN. The exit node validates that the
// source address belongs to the claimed origin and that the packet arrived
// from the next-hop peer that should be reachable for that origin, then
// "unwraps" it into the local stack.
const (
	NyUnicastProtoId       = 9
	NyUnicastHeaderSize    = 6
	NyUnicastDefaultHopLim = 64

	NyUnicastOffsetSubtype  = 0
	NyUnicastOffsetHopLimit = 1
	NyUnicastOffsetDst      = 2
	NyUnicastOffsetSrc      = 4
)

type NyUnicastSubtype byte

const (
	// NyUnicastSubtypeExit wraps an IPv4/IPv6 packet that should leave the
	// nylon mesh at the destination node. The subtype payload is the
	// untouched inner packet.
	NyUnicastSubtypeExit NyUnicastSubtype = 1
)

// ExitFilterSnapshot is an immutable view of all the state the exit filter
// needs to make a decision: local identity, exit configuration, the binary
// node-id mapping, per-node source-address ownership, and the binary-keyed
// next-hop forwarding table.
//
// It is rebuilt by the dispatch goroutine on three occasions: at startup,
// after every central config apply, and after every route mutation. The
// filter only ever loads (and never mutates) the pointer. This keeps the
// dataplane path lock-free and entirely free of references into the live
// LocalCfg / CentralCfg / RouterState structures.
type ExitFilterSnapshot struct {
	// Local identity.
	LocalId    state.NodeId
	LocalIdBin state.NodeIdBin

	// Local capabilities.
	AdvertiseExitNode bool
	ExitNode          state.NodeId    // empty => not configured to use an exit
	ExitNodeBin       state.NodeIdBin // InvalidNodeIdBin if ExitNode is empty or unmapped

	// Per-node exit source address ownership. Used at the exit node to
	// validate that the inner packet source matches the origin claimed in
	// the header. Only the node's directly-assigned Addresses count;
	// advertised prefixes / anycast addresses are intentionally excluded.
	NodeAddrs map[state.NodeIdBin]map[netip.Addr]struct{}

	// Binary -> string lookup, copied out of NodeIdMap for trace/log paths
	// that need readable names.
	BinNames map[state.NodeIdBin]state.NodeId

	// NodeForward maps a destination node's binary id to the next-hop
	// peer (and its NodeId for trace output). Built off the currently
	// selected routes — every entry has a finite metric and is reachable
	// via a peer that is not the local node.
	NodeForward map[state.NodeIdBin]RouteTableEntry
}

// rebuildExitFilterSnapshot constructs a fresh snapshot from the current
// LocalCfg + CentralCfg + NodeIdMap and the currently selected routes.
// Must be called on the dispatch goroutine.
func (n *Nylon) rebuildExitFilterSnapshot(idMap *state.NodeIdMap) *ExitFilterSnapshot {
	snap := &ExitFilterSnapshot{
		LocalId:           n.LocalCfg.Id,
		AdvertiseExitNode: n.LocalCfg.AdvertiseExitNode,
		ExitNode:          n.LocalCfg.ExitNode,
		NodeAddrs:         make(map[state.NodeIdBin]map[netip.Addr]struct{}),
		BinNames:          make(map[state.NodeIdBin]state.NodeId),
		NodeForward:       make(map[state.NodeIdBin]RouteTableEntry),
	}
	if bin, ok := idMap.ToBin(n.LocalCfg.Id); ok {
		snap.LocalIdBin = bin
	}
	if snap.ExitNode != "" {
		if bin, ok := idMap.ToBin(snap.ExitNode); ok {
			snap.ExitNodeBin = bin
		}
	}
	addAddrs := func(id state.NodeId, addrs []netip.Addr) {
		bin, ok := idMap.ToBin(id)
		if !ok {
			return
		}
		snap.BinNames[bin] = id
		if len(addrs) == 0 {
			return
		}
		m := snap.NodeAddrs[bin]
		if m == nil {
			m = make(map[netip.Addr]struct{}, len(addrs))
			snap.NodeAddrs[bin] = m
		}
		for _, a := range addrs {
			m[a] = struct{}{}
		}
	}
	for _, r := range n.CentralCfg.Routers {
		addAddrs(r.Id, r.Addresses)
	}
	for _, c := range n.CentralCfg.Clients {
		addAddrs(c.Id, c.Addresses)
	}

	// Build the per-node forwarding table. We look each node's assigned
	// addresses up in the prefix-keyed ForwardTable (already atomic,
	// already aggregation-aware) — iterating RouterState.Routes by
	// NodeId would miss nodes whose /32 has been folded into a Babel
	// supernet. ForwardTable is rebuilt before this snapshot, so the
	// lookup sees the latest entries.
	if ft := n.router.ForwardTable.Load(); ft != nil {
		add := func(id state.NodeId, addrs []netip.Addr) {
			if id == n.LocalCfg.Id {
				return
			}
			bin, ok := idMap.ToBin(id)
			if !ok {
				return
			}
			if _, exists := snap.NodeForward[bin]; exists {
				return
			}
			for _, addr := range addrs {
				entry, ok := ft.Lookup(addr)
				if !ok || entry.Blackhole || entry.Peer == nil {
					continue
				}
				snap.NodeForward[bin] = entry
				break
			}
		}
		for _, r := range n.CentralCfg.Routers {
			add(r.Id, r.Addresses)
		}
		for _, c := range n.CentralCfg.Clients {
			add(c.Id, c.Addresses)
		}
	}
	return snap
}

// refreshNodeBindings recomputes both the NodeIdMap and ExitFilter snapshots
// from the current config and routing state, and stores them atomically.
// Must be called on the dispatch goroutine after any change that could
// affect either the binary-id assignment (CentralCfg) or the per-node
// next-hop (selected routes / local exit settings).
func (n *Nylon) refreshNodeBindings() error {
	idMap, err := state.BuildNodeIdMap(&n.CentralCfg)
	if err != nil {
		return err
	}
	n.NodeIdMap.Store(idMap)
	n.ExitFilter.Store(n.rebuildExitFilterSnapshot(idMap))
	return nil
}

// refreshExitFilter rebuilds just the ExitFilter snapshot using the current
// NodeIdMap. Cheaper than refreshNodeBindings; appropriate when only the
// routing state has changed.
func (n *Nylon) refreshExitFilter() {
	idMap := n.NodeIdMap.Load()
	if idMap == nil {
		return
	}
	n.ExitFilter.Store(n.rebuildExitFilterSnapshot(idMap))
}

// nyUnicastHeader is a parsed view of a NyUnicast payload header.
type nyUnicastHeader struct {
	subtype  NyUnicastSubtype
	hopLimit uint8
	dst      state.NodeIdBin
	src      state.NodeIdBin
}

func parseNyUnicastHeader(payload []byte) (nyUnicastHeader, error) {
	if len(payload) < NyUnicastHeaderSize {
		return nyUnicastHeader{}, errors.New("nylon: unicast packet shorter than header")
	}
	return nyUnicastHeader{
		subtype:  NyUnicastSubtype(payload[NyUnicastOffsetSubtype]),
		hopLimit: payload[NyUnicastOffsetHopLimit],
		dst:      state.NodeIdBin(binary.BigEndian.Uint16(payload[NyUnicastOffsetDst : NyUnicastOffsetDst+2])),
		src:      state.NodeIdBin(binary.BigEndian.Uint16(payload[NyUnicastOffsetSrc : NyUnicastOffsetSrc+2])),
	}, nil
}

// writeNyUnicastHeader writes the fixed 6-byte payload header at the start of
// buf. buf must be at least NyUnicastHeaderSize long.
func writeNyUnicastHeader(buf []byte, h nyUnicastHeader) {
	buf[NyUnicastOffsetSubtype] = byte(h.subtype)
	buf[NyUnicastOffsetHopLimit] = h.hopLimit
	binary.BigEndian.PutUint16(buf[NyUnicastOffsetDst:NyUnicastOffsetDst+2], uint16(h.dst))
	binary.BigEndian.PutUint16(buf[NyUnicastOffsetSrc:NyUnicastOffsetSrc+2], uint16(h.src))
}

// wrapExitPacket re-frames the current IP packet in `packet` as a NyUnicast
// exit-encap packet bound for `exit` from `origin`. Mutates packet in place.
func wrapExitPacket(packet *device.TCElement, exit, origin state.NodeIdBin) error {
	if exit == state.InvalidNodeIdBin || origin == state.InvalidNodeIdBin {
		return errors.New("nylon: invalid node id in exit header")
	}

	origLen := len(packet.Packet)
	headerLen := device.PolyHeaderSize + NyUnicastHeaderSize
	totalLen := headerLen + origLen
	if totalLen > len(packet.Buffer)-device.MessageTransportHeaderSize {
		return errors.New("nylon: packet too large for exit encapsulation")
	}

	// Slide inner IP packet right by headerLen and rebase Packet to point
	// at the new outer header. We use the message-transport offset so the
	// downstream encryptor sees the same Buffer layout as for any other
	// packet.
	buf := packet.Buffer[device.MessageTransportHeaderSize : device.MessageTransportHeaderSize+totalLen]
	copy(buf[headerLen:], packet.Packet)
	packet.Packet = buf

	packet.SetIPVersion(NyUnicastProtoId)
	packet.SetLength(uint16(totalLen))
	writeNyUnicastHeader(packet.Payload(), nyUnicastHeader{
		subtype:  NyUnicastSubtypeExit,
		hopLimit: NyUnicastDefaultHopLim,
		dst:      exit,
		src:      origin,
	})
	return nil
}

// exitOriginArrivedFromExpectedPeer verifies that an inbound exit packet
// arrived from the next-hop peer that would be used to reach `origin`. This
// prevents another peer from spoofing exit packets on someone else's behalf
// (modulo full-path attestation, which would require per-packet signing).
// Uses only the precomputed snapshot — never touches RouterState directly.
func exitOriginArrivedFromExpectedPeer(snap *ExitFilterSnapshot, origin state.NodeIdBin, fromPeer *device.Peer) bool {
	if fromPeer == nil {
		return false
	}
	entry, ok := snap.NodeForward[origin]
	if !ok || entry.Peer == nil {
		return false
	}
	return fromPeer.GetPublicKey() == entry.Peer.GetPublicKey()
}

func packetSrc(packet []byte) (netip.Addr, error) {
	return packetAddr(packet, true)
}

func packetDst(packet []byte) (netip.Addr, error) {
	return packetAddr(packet, false)
}

func packetAddr(packet []byte, src bool) (netip.Addr, error) {
	if len(packet) == 0 {
		return netip.Addr{}, errors.New("empty inner packet")
	}
	switch packet[0] >> 4 {
	case 4:
		offset := device.IPv4offsetDst
		if src {
			offset = device.IPv4offsetSrc
		}
		if len(packet) < offset+net.IPv4len {
			return netip.Addr{}, errors.New("short IPv4 packet")
		}
		return netip.AddrFrom4([4]byte(packet[offset : offset+net.IPv4len])), nil
	case 6:
		offset := device.IPv6offsetDst
		if src {
			offset = device.IPv6offsetSrc
		}
		if len(packet) < offset+net.IPv6len {
			return netip.Addr{}, errors.New("short IPv6 packet")
		}
		return netip.AddrFrom16([16]byte(packet[offset : offset+net.IPv6len])), nil
	default:
		return netip.Addr{}, errors.New("inner packet is not IP")
	}
}

// handleExitPacket dispatches a NyUnicast / NyUnicastSubtypeExit packet:
// transit if we are not the destination, decap back to the local stack if
// we are. Reads only the supplied snapshot, never the live config.
func (n *Nylon) handleExitPacket(packet *device.TCElement, snap *ExitFilterSnapshot, h nyUnicastHeader) (device.TCAction, error) {
	t := n.Trace
	if h.hopLimit == 0 {
		return device.TcDrop, errors.New("exit packet hop limit exceeded")
	}

	if h.dst != snap.LocalIdBin {
		entry, ok := snap.NodeForward[h.dst]
		if !ok || entry.Peer == nil {
			return device.TcDrop, fmt.Errorf("no route to exit node bin=%d", h.dst)
		}
		packet.Payload()[NyUnicastOffsetHopLimit]--
		packet.ToPeer = entry.Peer
		packet.Priority = device.TcMediumPriority
		if n.DBG_trace_tc {
			t.Submit(fmt.Sprintf("ExitTransit: origin %s exit %s via %s\n", snap.BinNames[h.src], snap.BinNames[h.dst], entry.Nh))
		}
		return device.TcForward, nil
	}

	// Terminal: this is the exit node for the packet.
	if !snap.AdvertiseExitNode {
		return device.TcDrop, errors.New("local node is not advertising exit service")
	}
	originName, ok := snap.BinNames[h.src]
	if !ok {
		return device.TcDrop, fmt.Errorf("unknown exit origin bin=%d", h.src)
	}
	if !exitOriginArrivedFromExpectedPeer(snap, h.src, packet.FromPeer) {
		return device.TcDrop, fmt.Errorf("exit packet origin %s did not arrive from expected peer", originName)
	}
	inner := packet.Payload()[NyUnicastHeaderSize:]
	src, err := packetSrc(inner)
	if err != nil {
		return device.TcDrop, err
	}
	if !nodeOwnsExitSourceAddr(snap, h.src, src) {
		return device.TcDrop, fmt.Errorf("source %s is not owned by origin node %s", src, originName)
	}
	dst, err := packetDst(inner)
	if err != nil {
		return device.TcDrop, err
	}
	if n.DBG_trace_tc {
		t.Submit(fmt.Sprintf("ExitDecap: origin %s %s -> %s\n", originName, src, dst))
	}
	// Repoint Packet at the inner IP packet so subsequent filters /
	// system routing see it as a regular IP packet from this node.
	copy(packet.Packet[:len(inner)], inner)
	packet.Packet = packet.Packet[:len(inner)]
	packet.ParsePacket()
	return device.TcBounce, nil
}

// nodeOwnsExitSourceAddr reports whether `addr` is one of the directly
// assigned addresses of the node identified by `origin`. Advertised prefixes
// and anycast addresses are intentionally excluded — only an address listed
// in the node's Addresses field counts.
func nodeOwnsExitSourceAddr(snap *ExitFilterSnapshot, origin state.NodeIdBin, addr netip.Addr) bool {
	addrs := snap.NodeAddrs[origin]
	if len(addrs) == 0 {
		return false
	}
	_, ok := addrs[addr]
	return ok
}
