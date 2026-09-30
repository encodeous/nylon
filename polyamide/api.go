// Package polyamide defines the packet transport API.
package polyamide

import (
	"context"
	"errors"
	"time"
)

var (
	ErrClosed          = errors.New("transport closed")
	ErrStarted         = errors.New("transport already started")
	ErrNotStarted      = errors.New("transport not started")
	ErrInvalidKey      = errors.New("invalid transport key")
	ErrInvalidHandle   = errors.New("foreign or retired transport handle")
	ErrInvalidEndpoint = errors.New("invalid transport endpoint")
	ErrMessageTooLarge = errors.New("control message too large")
	ErrUnsupported     = errors.New("unsupported transport feature")
	ErrNoEndpoint      = errors.New("peer has no configured endpoint")
)

// Peers are compared by identity. Transport implementations must use pointers, and
// PreparePeer returns a new handle when a peer's public key changes.
type Peer interface {
	Transport() Transport
	ID() string
	PublicKey() PublicKey
}

// Endpoints are created by their transport. Nil selects the peer's configured endpoints.
type Endpoint interface {
	Address() string
}

const KeySize = 32

type PublicKey [KeySize]byte
type PrivateKey [KeySize]byte

type PeerConfig struct {
	ID        string
	PublicKey PublicKey
	Passive   bool
}

// The transport owns Payload. Copy it to keep it after the callback returns.
type ControlMessage struct {
	Payload  []byte
	From     Peer
	Endpoint Endpoint
}

// Transports can call callbacks at the same time. Do not change peer configuration,
// call Start or Close, or wait for Nylon's dispatcher from a callback.
type Hooks struct {
	RouteBatch  TCFilter
	Control     func(ControlMessage)
	DeliverHost func(packets [][]byte) error
	// EndpointLearned reports a packet from a peer at an endpoint that the peer
	// was not configured with, such as after the peer roams.
	EndpointLearned func(peer Peer, endpoint Endpoint)
}

// Byte counts include transport headers and reset when a peer handle is replaced.
type PeerStats struct {
	Peer              Peer
	LastReceived      time.Time
	LastHandshake     time.Time
	TxBytes           uint64
	RxBytes           uint64
	PreferredEndpoint Endpoint
	Keepalive         time.Duration
}

// The caller can reuse packet bytes after a send returns.
type Transport interface {
	PrepareEndpoint(context.Context, string) (Endpoint, error)
	// Cancelling the context stops the transport. Start does not wait for peers to connect.
	Start(context.Context, Hooks) error
	// The transport keeps replaced peer handles until RemovePeer.
	PreparePeer(context.Context, PeerConfig) (Peer, error)
	// SetEndpoints replaces the peer's addresses. The first is the default for packets
	// sent without an endpoint. An empty list allows passive roaming.
	SetEndpoints(context.Context, Peer, []Endpoint) error
	RemovePeer(context.Context, Peer) error
	// ControlMTU reports payload capacity, not discovered path MTU.
	ControlMTU(Peer, Endpoint) (int, error)
	SendControl(context.Context, Peer, Endpoint, []byte) error
	SendPackets(context.Context, []OutboundPacket) error
	// Peers returns statistics owned by the caller. Values may be read at different times.
	Peers() []PeerStats
	// SetHostMTU reports the host interface MTU. It can be called before Start.
	SetHostMTU(mtu int)
	// Close waits for transport workers and callbacks, even if startup failed.
	Close() error
}

type OutboundPacket struct {
	Bytes    []byte
	To       Peer
	Endpoint Endpoint
	Priority TCPriority
}
