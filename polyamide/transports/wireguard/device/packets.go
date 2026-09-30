package device

import (
	"encoding/binary"
	"net"

	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// InboundPacket is a decrypted packet. Packet is valid only during the Receiver call.
type InboundPacket struct {
	Packet   []byte
	Endpoint conn.Endpoint
}

// Hooks let the owner of a device handle its packets and endpoints.
type Hooks struct {
	// Receive handles decrypted packets from peer. Without it, the device writes
	// packets from allowed source addresses to its TUN, like upstream WireGuard.
	Receive func(peer *Peer, packets []InboundPacket)
	// EndpointLearned reports an authenticated packet from an endpoint that is not
	// in the peer's endpoint list. It runs on the receive path and must not block.
	EndpointLearned func(peer *Peer, endpoint conn.Endpoint)
}

// OutboundPacket is a plaintext packet stored at MessageTransportHeaderSize in a
// buffer from GetMessageBuffer.
type OutboundPacket struct {
	Buffer   *[MaxMessageSize]byte
	Size     int
	Endpoint conn.Endpoint // nil uses the peer's endpoints
}

// SendPackets encrypts and sends packets to peer. It takes ownership of every buffer.
func (peer *Peer) SendPackets(packets []OutboundPacket) {
	device := peer.device
	if !peer.isRunning.Load() {
		for _, packet := range packets {
			device.PutMessageBuffer(packet.Buffer)
		}
		return
	}
	elems := device.GetOutboundElementsContainer()
	for _, packet := range packets {
		elem := device.GetOutboundElement()
		elem.nonce = 0
		elem.endpoint = packet.Endpoint
		elem.buffer = packet.Buffer
		elem.packet = packet.Buffer[MessageTransportHeaderSize : MessageTransportHeaderSize+packet.Size]
		elems.elems = append(elems.elems, elem)
	}
	peer.StagePackets(elems)
	peer.SendStagedPackets()
}

// allowedPacket trims packet to its IP length and checks that peer may use its source address.
func (device *Device) allowedPacket(peer *Peer, packet []byte) ([]byte, bool) {
	switch packet[0] >> 4 {
	case 4:
		if len(packet) < ipv4.HeaderLen {
			return nil, false
		}
		length := binary.BigEndian.Uint16(packet[IPv4offsetTotalLength : IPv4offsetTotalLength+2])
		if int(length) > len(packet) || int(length) < ipv4.HeaderLen {
			return nil, false
		}
		packet = packet[:length]
		if device.Allowedips.Lookup(packet[IPv4offsetSrc:IPv4offsetSrc+net.IPv4len]) != peer {
			device.Log.Verbosef("IPv4 packet with disallowed source address from %v", peer)
			return nil, false
		}
	case 6:
		if len(packet) < ipv6.HeaderLen {
			return nil, false
		}
		length := binary.BigEndian.Uint16(packet[IPv6offsetPayloadLength:IPv6offsetPayloadLength+2]) + ipv6.HeaderLen
		if int(length) > len(packet) {
			return nil, false
		}
		packet = packet[:length]
		if device.Allowedips.Lookup(packet[IPv6offsetSrc:IPv6offsetSrc+net.IPv6len]) != peer {
			device.Log.Verbosef("IPv6 packet with disallowed source address from %v", peer)
			return nil, false
		}
	default:
		device.Log.Verbosef("Packet with invalid IP version from %v", peer)
		return nil, false
	}
	return packet, true
}
