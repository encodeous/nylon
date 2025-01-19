package state

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type CtlLink interface {
	NetLink
	WriteMsg(m proto.Message) error
	ReadMsg(proto.Message) error
	// IsRemote is true if the link is remotely initiated
	IsRemote() bool
	// Close the link
	Close()
}

type DpLink interface {
	NetLink
}

type NetLink interface {
	Id() uuid.UUID
	Metric() uint16
}
