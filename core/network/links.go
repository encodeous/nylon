package network

import (
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type CtlLink interface {
	NetLink
	SendPacket(m proto.Message) error
	ReceivePacket() (proto.Message, error)
}

type DpLink interface {
	NetLink
}

type NetLink interface {
	Id() uuid.UUID
	Metric() uint16
}
