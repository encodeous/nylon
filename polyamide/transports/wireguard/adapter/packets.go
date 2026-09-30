package adapter

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/device"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun"
)

// Control messages use IP version 8, followed by a big-endian payload length.
const (
	controlVersion    = 8
	controlHeaderSize = 3
	maxControlPayload = device.MaxContentSize - controlHeaderSize
)

// controlPayload returns the payload of a control message.
func controlPayload(packet []byte) ([]byte, bool) {
	if len(packet) < controlHeaderSize || packet[0]>>4 != controlVersion {
		return nil, false
	}
	end := controlHeaderSize + int(binary.BigEndian.Uint16(packet[1:controlHeaderSize]))
	if end > len(packet) {
		return nil, false
	}
	return packet[controlHeaderSize:end:end], true
}

type routeBatch struct {
	packets   []polyamide.TCElement
	decisions []polyamide.TCDecision
	bounced   [][]byte // packets to deliver to the host
}

// receive handles decrypted packets from raw. The device releases their buffers
// after it returns.
func (t *Transport) receive(raw *device.Peer, packets []device.InboundPacket) {
	if t.closed.Load() || !t.started.Load() {
		return
	}
	t.mu.RLock()
	from := t.byRaw[raw]
	t.mu.RUnlock()
	if from == nil || from.retired.Load() {
		return
	}
	hooks := t.hooks.Load()
	b := t.batchPool.Get().(*routeBatch)
	defer func() {
		clear(b.packets)
		clear(b.decisions)
		clear(b.bounced)
		b.packets, b.decisions, b.bounced = b.packets[:0], b.decisions[:0], b.bounced[:0]
		t.batchPool.Put(b)
	}()
	for _, packet := range packets {
		if packet.Endpoint == nil {
			continue
		}
		source := t.rememberEndpoint(packet.Endpoint)
		if payload, ok := controlPayload(packet.Packet); ok {
			if hooks.Control != nil {
				hooks.Control(polyamide.ControlMessage{Payload: payload, From: from, Endpoint: source})
			}
			continue
		}
		if data, ok := polyamide.IPBytes(packet.Packet); ok {
			b.packets = append(b.packets, polyamide.TCElement{Bytes: data, From: from, Endpoint: source})
			b.decisions = append(b.decisions, polyamide.TCDecision{})
		}
	}
	if len(b.packets) == 0 || hooks.RouteBatch == nil {
		return
	}
	hooks.RouteBatch(b.packets, b.decisions)
	if err := polyamide.DispatchBatch(t.runContext, b.packets, b.decisions); err != nil && t.runContext.Err() == nil && !t.closed.Load() {
		t.opts.Logger.Warn("failed to dispatch packet batch", "err", err)
	}
	for i, decision := range b.decisions {
		if decision.Action == polyamide.TcBounce {
			b.bounced = append(b.bounced, b.packets[i].Bytes)
		}
	}
	if len(b.bounced) != 0 && hooks.DeliverHost != nil {
		if err := hooks.DeliverHost(b.bounced); err != nil && !t.closed.Load() {
			t.opts.Logger.Warn("failed to deliver packets to host", "err", err)
		}
	}
}

func (t *Transport) SendControl(ctx context.Context, value polyamide.Peer, selected polyamide.Endpoint, payload []byte) error {
	if err := t.beginSubmission(); err != nil {
		return err
	}
	defer t.submissions.Done()
	p, err := t.checkPeer(value)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !t.started.Load() {
		return polyamide.ErrNotStarted
	}
	path, err := t.resolveEndpoint(selected)
	if err != nil {
		return err
	}
	if len(payload) > maxControlPayload {
		return polyamide.ErrMessageTooLarge
	}
	if err := t.ensureEndpoint(p, path); err != nil {
		return err
	}
	buffer := t.dev.GetMessageBuffer()
	packet := buffer[device.MessageTransportHeaderSize:]
	packet[0] = controlVersion << 4
	binary.BigEndian.PutUint16(packet[1:controlHeaderSize], uint16(len(payload)))
	copy(packet[controlHeaderSize:], payload)
	p.raw.SendPackets([]device.OutboundPacket{{Buffer: buffer, Size: controlHeaderSize + len(payload), Endpoint: path}})
	return nil
}

func (t *Transport) SendPackets(ctx context.Context, packets []polyamide.OutboundPacket) error {
	if err := t.beginSubmission(); err != nil {
		return err
	}
	defer t.submissions.Done()
	if t.closed.Load() {
		return polyamide.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !t.started.Load() {
		return polyamide.ErrNotStarted
	}
	// Invalid packets are skipped so they cannot drop the rest of the batch.
	var result error
	submissions := make([]submission, 0, len(packets))
	for _, packet := range packets {
		s, err := t.prepareSubmission(packet)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		submissions = append(submissions, s)
	}
	// Send high-priority packets first, then group by peer in batches the bind can send.
	slices.SortStableFunc(submissions, func(a, b submission) int { return cmp.Compare(b.priority, a.priority) })
	byPeer := make(map[*peer][]device.OutboundPacket)
	var order []*peer
	for _, packet := range submissions {
		if _, ok := byPeer[packet.peer]; !ok {
			order = append(order, packet.peer)
		}
		buffer := t.dev.GetMessageBuffer()
		copy(buffer[device.MessageTransportHeaderSize:], packet.data)
		byPeer[packet.peer] = append(byPeer[packet.peer], device.OutboundPacket{Buffer: buffer, Size: len(packet.data), Endpoint: packet.endpoint})
	}
	for _, p := range order {
		for batch := range slices.Chunk(byPeer[p], t.dev.BatchSize()) {
			p.raw.SendPackets(batch)
		}
	}
	return result
}

type submission struct {
	data     []byte
	peer     *peer
	endpoint conn.Endpoint
	priority polyamide.TCPriority
}

// prepareSubmission validates packet and resolves its peer and endpoint.
func (t *Transport) prepareSubmission(packet polyamide.OutboundPacket) (submission, error) {
	data, valid := polyamide.IPBytes(packet.Bytes)
	if !valid {
		return submission{}, fmt.Errorf("malformed outgoing IP packet")
	}
	if len(data) > device.MaxContentSize {
		return submission{}, polyamide.ErrMessageTooLarge
	}
	if packet.Priority != polyamide.TcNormalPriority && packet.Priority != polyamide.TcHighPriority {
		return submission{}, fmt.Errorf("invalid packet priority")
	}
	p, err := t.checkPeer(packet.To)
	if err != nil {
		return submission{}, err
	}
	endpoint, err := t.resolveEndpoint(packet.Endpoint)
	if err != nil {
		return submission{}, err
	}
	if err := t.ensureEndpoint(p, endpoint); err != nil {
		return submission{}, err
	}
	return submission{data: data, peer: p, endpoint: endpoint, priority: packet.Priority}, nil
}

func (t *Transport) beginSubmission() error {
	t.submitMu.Lock()
	defer t.submitMu.Unlock()
	if t.closed.Load() {
		return polyamide.ErrClosed
	}
	t.submissions.Add(1)
	return nil
}

// endpointLearned reports a new endpoint for raw once per address change.
func (t *Transport) endpointLearned(raw *device.Peer, native conn.Endpoint) {
	hooks := t.hooks.Load()
	if t.closed.Load() || hooks == nil || hooks.EndpointLearned == nil {
		return
	}
	t.mu.RLock()
	p := t.byRaw[raw]
	t.mu.RUnlock()
	if p == nil || p.retired.Load() {
		return
	}
	destination := native.DstIPPort()
	if last := p.learned.Swap(&destination); last != nil && *last == destination {
		return
	}
	hooks.EndpointLearned(p, t.rememberEndpoint(native))
}

// SetHostMTU passes the host MTU to the engine through its TUN event reader.
func (t *Transport) SetHostMTU(mtu int) { t.host.setMTU(mtu) }

// hostSink stands in for the engine's TUN. The transport delivers host packets
// through Hooks.DeliverHost, so the engine never reads or writes it. It only
// reports the host MTU, and never reports the interface going down.
type hostSink struct {
	batchSize int
	mtu       atomic.Int32
	closed    chan struct{}
	events    chan tun.Event
	mu        sync.Mutex // protects events against Close
	done      bool
}

func newHostSink(batchSize int) *hostSink {
	h := &hostSink{batchSize: batchSize, closed: make(chan struct{}), events: make(chan tun.Event, 1)}
	h.mtu.Store(device.DefaultMTU)
	return h
}

func (h *hostSink) setMTU(mtu int) {
	if h.mtu.Swap(int32(mtu)) == int32(mtu) {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.done {
		return
	}
	select {
	case h.events <- tun.EventMTUUpdate:
	default: // an update is already pending, and the reader loads the latest MTU
	}
}
func (*hostSink) File() *os.File             { return nil }
func (*hostSink) Name() (string, error)      { return "polyamide-wireguard", nil }
func (h *hostSink) MTU() (int, error)        { return int(h.mtu.Load()), nil }
func (h *hostSink) BatchSize() int           { return h.batchSize }
func (h *hostSink) Events() <-chan tun.Event { return h.events }
func (h *hostSink) Read(_ [][]byte, _ []int, _ int) (int, error) {
	<-h.closed
	return 0, os.ErrClosed
}
func (*hostSink) Write(buffers [][]byte, _ int) (int, error) { return len(buffers), nil }
func (h *hostSink) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.done {
		h.done = true
		close(h.closed)
		close(h.events)
	}
	return nil
}
