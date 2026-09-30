package polyamide

import (
	"encoding/binary"
	"testing"
)

func checksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i < len(data); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(data[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = sum&65535 + sum>>16
	}
	return ^uint16(sum)
}
func TestIPv4OptionsTTLAndPadding(t *testing.T) {
	data := make([]byte, 32)
	data[0], data[8], data[9] = 0x46, 2, 17
	binary.BigEndian.PutUint16(data[2:4], 28)
	copy(data[12:20], []byte{10, 0, 0, 1, 10, 0, 0, 2})
	binary.BigEndian.PutUint16(data[10:12], checksum(data[:24]))
	ip, ok := IPBytes(data)
	if !ok || len(ip) != 28 || cap(ip) != 28 {
		t.Fatal("incorrect packet extent")
	}
	p := TCElement{Bytes: ip}
	if p.Source().String() != "10.0.0.1" || p.Destination().String() != "10.0.0.2" {
		t.Fatal("incorrect addresses")
	}
	for want := uint8(1); ; want-- {
		p.DecrementTTL()
		if p.TTL() != want || checksum(ip[:24]) != 0 {
			t.Fatal("invalid TTL or checksum")
		}
		if want == 0 {
			break
		}
	}
	p.DecrementTTL()
	if p.TTL() != 0 || checksum(ip[:24]) != 0 {
		t.Fatal("TTL underflow")
	}
}
func TestMalformedIPAndIPv6(t *testing.T) {
	for _, data := range [][]byte{nil, {0x45}, {0x80}, make([]byte, 20)} {
		if _, ok := IPBytes(data); ok {
			t.Fatal("accepted malformed packet")
		}
	}
	ip := make([]byte, 20)
	ip[0] = 0x46
	binary.BigEndian.PutUint16(ip[2:4], 20)
	if _, ok := IPBytes(ip); ok {
		t.Fatal("accepted options exceeding packet")
	}
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], 21)
	if _, ok := IPBytes(ip); ok {
		t.Fatal("accepted truncated packet")
	}
	v6 := make([]byte, 48)
	v6[0] = 0x60
	v6[7] = 1
	binary.BigEndian.PutUint16(v6[4:6], 4)
	data, ok := IPBytes(v6)
	if !ok || len(data) != 44 {
		t.Fatal("invalid IPv6 extent")
	}
	p := TCElement{Bytes: data}
	p.DecrementTTL()
	p.DecrementTTL()
	if p.TTL() != 0 {
		t.Fatal("IPv6 TTL underflow")
	}
}
