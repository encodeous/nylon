package device

import (
	"encoding/binary"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"net"
	"net/netip"
)

// poly packets use other "IP Versions"
const (
	PolyHeaderSize          = 3
	PolyOffsetPayloadLength = 1
)

func (elem *TCElement) InitPacket(ver int, len uint16) {
	elem.Packet = elem.Buffer[MessageTransportHeaderSize : MessageTransportHeaderSize+len]
	elem.SetIPVersion(ver)
	elem.SetLength(len)
}

func (elem *TCElement) ParsePacket() {
	elem.Packet = elem.Buffer[MessageTransportHeaderSize:]
	l := elem.GetLength()
	elem.Packet = elem.Buffer[MessageTransportHeaderSize : MessageTransportHeaderSize+l]
}

func (elem *TCElement) Incoming() bool {
	return elem.FromPeer != nil
}

func (elem *TCElement) GetIPVersion() int {
	return int(elem.Packet[0] >> 4)
}

func (elem *TCElement) GetSrcBytes() []byte {
	ver := elem.GetIPVersion()
	if ver == 4 {
		return elem.Packet[IPv4offsetSrc : IPv4offsetSrc+net.IPv4len]
	} else if ver == 6 {
		return elem.Packet[IPv6offsetSrc : IPv6offsetSrc+net.IPv6len]
	}
	return nil
}

func (elem *TCElement) GetDstBytes() []byte {
	ver := elem.GetIPVersion()
	if ver == 4 {
		return elem.Packet[IPv4offsetDst : IPv4offsetDst+net.IPv4len]
	} else if ver == 6 {
		return elem.Packet[IPv6offsetDst : IPv6offsetDst+net.IPv6len]
	}
	return nil
}

func (elem *TCElement) GetSrc() netip.Addr {
	ver := elem.GetIPVersion()
	b := elem.GetSrcBytes()
	if ver == 4 {
		return netip.AddrFrom4([4]byte(b))
	} else if ver == 6 {
		return netip.AddrFrom16([16]byte(b))
	}
	return netip.IPv4Unspecified()
}

func (elem *TCElement) SetSrc(addr netip.Addr) {
	bin, err := addr.MarshalBinary()
	if err != nil {
		panic(err)
	}
	copy(elem.GetSrcBytes(), bin)
}

func (elem *TCElement) GetDst() netip.Addr {
	ver := elem.GetIPVersion()
	b := elem.GetDstBytes()
	if ver == 4 {
		return netip.AddrFrom4([4]byte(b))
	} else if ver == 6 {
		return netip.AddrFrom16([16]byte(b))
	}
	return netip.IPv4Unspecified()
}

func (elem *TCElement) SetDst(addr netip.Addr) {
	bin, err := addr.MarshalBinary()
	if err != nil {
		panic(err)
	}
	copy(elem.GetDstBytes(), bin)
}

func (elem *TCElement) SetIPVersion(ver int) {
	elem.Packet[0] = byte(ver << 4)
}

// GetLength returns the length of the packet, including the header
func (elem *TCElement) GetLength() uint16 {
	ver := elem.GetIPVersion()
	if ver == 4 {
		field := elem.Packet[IPv4offsetTotalLength : IPv4offsetTotalLength+2]
		return binary.BigEndian.Uint16(field)
	} else if ver == 6 {
		field := elem.Packet[IPv6offsetPayloadLength : IPv6offsetPayloadLength+2]
		return binary.BigEndian.Uint16(field) + ipv6.HeaderLen
	} else {
		field := elem.Packet[PolyOffsetPayloadLength : PolyOffsetPayloadLength+2]
		return binary.BigEndian.Uint16(field) + PolyHeaderSize
	}
}

func (elem *TCElement) SetLength(len uint16) {
	ver := elem.GetIPVersion()
	if ver == 4 {
		binary.BigEndian.PutUint16(elem.Packet[IPv4offsetTotalLength:IPv4offsetTotalLength+2], len)
	} else if ver == 6 {
		binary.BigEndian.PutUint16(elem.Packet[IPv6offsetPayloadLength:IPv6offsetPayloadLength+2], len-ipv6.HeaderLen)
	} else {
		binary.BigEndian.PutUint16(elem.Packet[PolyOffsetPayloadLength:PolyOffsetPayloadLength+2], len-PolyHeaderSize)
	}
}

func (elem *TCElement) Payload() []byte {
	ver := elem.GetIPVersion()
	if ver == 4 {
		return elem.Packet[ipv4.HeaderLen:]
	} else if ver == 6 {
		return elem.Packet[ipv6.HeaderLen:]
	} else {
		return elem.Packet[PolyHeaderSize:]
	}
}

func (elem *TCElement) Validate() bool {
	if elem == nil || len(elem.Packet) == 0 {
		return false
	}
	ver := elem.GetIPVersion()
	if ver == 4 {
		if elem.GetLength() < ipv4.HeaderLen {
			return false
		}
	} else if ver == 6 {
		if elem.GetLength() < ipv6.HeaderLen {
			return false
		}
	} else {
		if elem.GetLength() < PolyHeaderSize {
			return false
		}
	}
	return true
}
