package state

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"time"
)

type CtlLink interface {
	NetLink
	WriteMsg(m proto.Message) error
	ReadMsg(proto.Message) error
	// Close the link
	Close()
}

type DpLink interface {
	NetLink
	Endpoint() *DpEndpoint
	// UpdatePing remote means that the ping update is from the finalizer
	UpdatePing(ping time.Duration)
	Renew()
}

type NetLink interface {
	Id() uuid.UUID
	Metric() uint16
	// IsRemote is true if the link is remotely initiated
	IsRemote() bool
	IsDead() bool
}

//func (k EdPublicKey) DeriveNylonAddr() net.IP {
//	// Nylon uses the RFC4193 Unique Local Unicast Address Space (FC00::/7) to assign network nodes, https://www.rfc-editor.org/rfc/rfc4193.html
//	hash := sha256.Sum256(k)
//	hash[0] = 0xfc
//	hash[0] |= 0b0000_0001
//	return hash[:16]
//}

type LinkPing struct {
	LinkId uuid.UUID
	Time   time.Time
}
