package device

import (
	"encoding/binary"
	"testing"
)

func TestParsePacketRejectsLengthBeyondReadBuffer(t *testing.T) {
	var buf [MaxMessageSize]byte
	packet := buf[MessageTransportHeaderSize : MessageTransportHeaderSize+2016]
	packet[0] = 6 << 4
	binary.BigEndian.PutUint16(packet[IPv6offsetPayloadLength:IPv6offsetPayloadLength+2], 2010)

	elem := &TCElement{
		Buffer: &buf,
		Packet: packet,
	}

	if elem.ParsePacket() {
		t.Fatal("expected oversized packet to be rejected")
	}
}

func TestParsePacketTrimsToAdvertisedLength(t *testing.T) {
	var buf [MaxMessageSize]byte
	packet := buf[MessageTransportHeaderSize : MessageTransportHeaderSize+128]
	packet[0] = 6 << 4
	binary.BigEndian.PutUint16(packet[IPv6offsetPayloadLength:IPv6offsetPayloadLength+2], 8)

	elem := &TCElement{
		Buffer: &buf,
		Packet: packet,
	}

	if !elem.ParsePacket() {
		t.Fatal("expected valid packet to parse")
	}
	if len(elem.Packet) != 48 {
		t.Fatalf("expected packet length 48, got %d", len(elem.Packet))
	}
}
