package polyamide

import (
	"encoding/binary"
	"net/netip"
)

// IPBytes checks the declared IP length and returns a view without trailing
// transport padding. IPv4 options are accepted. IPv6 jumbograms are unsupported.
func IPBytes(data []byte) ([]byte, bool) {
	if len(data) == 0 {
		return nil, false
	}
	var length int
	switch data[0] >> 4 {
	case 4:
		if len(data) < 20 {
			return nil, false
		}
		header := int(data[0]&15) * 4
		length = int(binary.BigEndian.Uint16(data[2:4]))
		if header < 20 || header > length {
			return nil, false
		}
	case 6:
		if len(data) < 40 {
			return nil, false
		}
		length = 40 + int(binary.BigEndian.Uint16(data[4:6]))
	default:
		return nil, false
	}
	if length > len(data) {
		return nil, false
	}
	return data[:length:length], true
}

func (p TCElement) Incoming() bool { return p.From != nil }
func (p TCElement) Version() int {
	if len(p.Bytes) == 0 {
		return 0
	}
	return int(p.Bytes[0] >> 4)
}
func (p TCElement) Source() netip.Addr {
	if p.Version() == 4 && len(p.Bytes) >= 20 {
		return netip.AddrFrom4([4]byte(p.Bytes[12:16]))
	}
	if p.Version() == 6 && len(p.Bytes) >= 40 {
		return netip.AddrFrom16([16]byte(p.Bytes[8:24]))
	}
	return netip.Addr{}
}
func (p TCElement) Destination() netip.Addr {
	if p.Version() == 4 && len(p.Bytes) >= 20 {
		return netip.AddrFrom4([4]byte(p.Bytes[16:20]))
	}
	if p.Version() == 6 && len(p.Bytes) >= 40 {
		return netip.AddrFrom16([16]byte(p.Bytes[24:40]))
	}
	return netip.Addr{}
}
func (p TCElement) TTL() uint8 {
	if p.Version() == 4 && len(p.Bytes) >= 20 {
		return p.Bytes[8]
	}
	if p.Version() == 6 && len(p.Bytes) >= 40 {
		return p.Bytes[7]
	}
	return 0
}

// DecrementTTL modifies a borrowed IP packet in place. A zero TTL is left at
// zero. For IPv4, it updates the header checksum.
func (p TCElement) DecrementTTL() {
	if p.TTL() == 0 {
		return
	}
	if p.Version() == 6 {
		p.Bytes[7]--
		return
	}
	p.Bytes[8]--
	checksum := uint32(binary.BigEndian.Uint16(p.Bytes[10:12])) + 0x100
	checksum += checksum >> 16
	binary.BigEndian.PutUint16(p.Bytes[10:12], uint16(checksum))
}

// HostDevice is owned by Nylon and is not managed by transports.
type HostDevice interface {
	Read(bufs [][]byte, sizes []int, offset int) (int, error)
	Write(bufs [][]byte, offset int) (int, error)
	MTU() (int, error)
	Name() (string, error)
	BatchSize() int
	Close() error
}
