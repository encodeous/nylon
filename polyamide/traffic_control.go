package polyamide

import (
	"context"
	"errors"
)

type TCAction uint8

const (
	TcDrop   TCAction = iota // zero decisions fail closed
	TcBounce                 // deliver to the host writer
	TcForward
)

type TCPriority uint8

const (
	TcNormalPriority TCPriority = iota
	TcHighPriority
)

// The source owns Bytes. Filters can edit the bytes but must not resize or keep them.
type TCElement struct {
	Bytes    []byte
	From     Peer
	Endpoint Endpoint
}

type TCDecision struct {
	Action   TCAction
	To       Peer
	Endpoint Endpoint
	Priority TCPriority
}

// TCFilter decides every packet in a batch. Each decision starts zeroed, so
// packets the filter leaves alone are dropped.
type TCFilter func(packets []TCElement, decisions []TCDecision)

// DispatchBatch sends forwarded packets through their target transports, one
// batch per transport. Forward decisions are cleared, so only bounces remain.
func DispatchBatch(ctx context.Context, packets []TCElement, decisions []TCDecision) error {
	if len(packets) == 0 {
		return nil
	}
	batches := make(map[Transport][]OutboundPacket)
	var result error
	for i, packet := range packets {
		decision := decisions[i]
		if decision.Action != TcForward {
			continue
		}
		if decision.To == nil || decision.To.Transport() == nil {
			decisions[i] = TCDecision{}
			result = errors.Join(result, ErrInvalidHandle)
			continue
		}
		target := decision.To.Transport()
		batches[target] = append(batches[target], OutboundPacket{Bytes: packet.Bytes, To: decision.To, Endpoint: decision.Endpoint, Priority: decision.Priority})
		decisions[i] = TCDecision{}
	}
	for transport, batch := range batches {
		result = errors.Join(result, transport.SendPackets(ctx, batch))
	}
	return result
}
