package polyamide

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"slices"
	"testing"
)

type dispatchTransport struct {
	Transport
	batches [][]OutboundPacket
	err     error
}

func (t *dispatchTransport) SendPackets(_ context.Context, packets []OutboundPacket) error {
	batch := make([]OutboundPacket, len(packets))
	for i, p := range packets {
		batch[i] = p
		batch[i].Bytes = append([]byte(nil), p.Bytes...)
	}
	t.batches = append(t.batches, batch)
	return t.err
}

type dispatchPeer struct {
	owner Transport
	id    string
}

func (p *dispatchPeer) Transport() Transport { return p.owner }
func (p *dispatchPeer) ID() string           { return p.id }
func (*dispatchPeer) PublicKey() PublicKey   { return PublicKey{} }

func TestDispatchBatchGroupsByTransport(t *testing.T) {
	a, b := new(dispatchTransport), new(dispatchTransport)
	pa1, pa2, pb := &dispatchPeer{a, "a1"}, &dispatchPeer{a, "a2"}, &dispatchPeer{b, "b"}
	endpoint := dispatchEndpoint("127.0.0.1:1234")
	expected := []TCDecision{
		{Action: TcForward, To: pa1, Endpoint: endpoint, Priority: TcHighPriority},
		{Action: TcForward, To: pb},
		{Action: TcForward, To: pa2},
		{Action: TcBounce},
		{Action: TcDrop},
	}
	packets := make([]TCElement, len(expected))
	decisions := slices.Clone(expected)
	for i := range packets {
		packets[i].Bytes = []byte{byte(i)}
	}
	require.NoError(t, DispatchBatch(context.Background(), packets, decisions))
	require.Len(t, a.batches, 1)
	require.Len(t, a.batches[0], 2)
	require.Same(t, pa1, a.batches[0][0].To)
	require.Same(t, pa2, a.batches[0][1].To)
	require.Equal(t, endpoint, a.batches[0][0].Endpoint)
	require.Equal(t, TcHighPriority, a.batches[0][0].Priority)
	require.Equal(t, []byte{0}, a.batches[0][0].Bytes)
	require.Equal(t, []byte{2}, a.batches[0][1].Bytes)
	require.Len(t, b.batches, 1)
	require.Len(t, b.batches[0], 1)
	require.Same(t, pb, b.batches[0][0].To)
	for _, d := range decisions[:3] {
		require.Equal(t, TCDecision{}, d, "already submitted packets must not be forwarded again")
	}
	require.Equal(t, expected[3:], decisions[3:])
}

func TestDispatchBatchErrorsDoNotPreventOtherBatches(t *testing.T) {
	failed := errors.New("send failed")
	a, b := &dispatchTransport{err: failed}, new(dispatchTransport)
	pa, pb := &dispatchPeer{a, "a"}, &dispatchPeer{b, "b"}
	packets := []TCElement{{Bytes: []byte{1}}, {Bytes: []byte{2}}, {Bytes: []byte{3}}}
	decisions := []TCDecision{{Action: TcForward, To: pa}, {Action: TcForward, To: pb}, {Action: TcForward}}
	err := DispatchBatch(context.Background(), packets, decisions)
	require.ErrorIs(t, err, failed)
	require.ErrorIs(t, err, ErrInvalidHandle)
	require.Len(t, a.batches, 1)
	require.Len(t, b.batches, 1)
	require.Equal(t, make([]TCDecision, 3), decisions)
}

type dispatchEndpoint string

func (e dispatchEndpoint) Address() string { return string(e) }
