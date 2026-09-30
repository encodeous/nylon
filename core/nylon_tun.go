package core

import (
	"errors"
	"fmt"
	"sync"

	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun"
)

// Reserve space for headers written by native TUN drivers.
const tunHeadroom = 16
const maxTUNPacketSize = 65535

func (n *Nylon) startTUNReader() {
	n.tunWorkers.Add(1)
	go func() {
		defer n.tunWorkers.Done()
		count := n.Tun.BatchSize()
		if count <= 0 {
			n.Cancel(errors.New("invalid TUN batch size"))
			return
		}
		storage := make([][]byte, count)
		sizes := make([]int, count)
		packets := make([][]byte, 0, count)
		for i := range storage {
			storage[i] = make([]byte, tunHeadroom+maxTUNPacketSize)
		}
		for n.Context.Err() == nil {
			read, readErr := n.Tun.Read(storage, sizes, tunHeadroom)
			if read < 0 || read > count {
				n.Cancel(errors.New("TUN returned invalid packet count"))
				return
			}
			for i, size := range sizes[:read] {
				if size < 0 || size > maxTUNPacketSize {
					n.Cancel(errors.New("invalid TUN packet size"))
					return
				}
				if size != 0 {
					packets = append(packets, storage[i][tunHeadroom:tunHeadroom+size])
				}
			}
			if len(packets) != 0 {
				if err := n.routeTUNPackets(packets); err != nil && n.Context.Err() == nil {
					n.Log.Warn("failed to submit TUN packets", "err", err)
				}
			}
			clear(packets)
			packets = packets[:0]
			if readErr != nil {
				// Like upstream WireGuard, a read with too many segments only drops packets.
				if errors.Is(readErr, tun.ErrTooManySegments) {
					n.Log.Debug("dropped packets from multi-segment TUN read", "err", readErr)
					continue
				}
				if n.Context.Err() == nil {
					n.Cancel(fmt.Errorf("read TUN packets: %w", readErr))
				}
				return
			}
		}
	}()
}

// watchTUNEvents passes host MTU changes to the transports and logs up and down
// events. Transports keep running while the host interface is down.
func (n *Nylon) watchTUNEvents() {
	host, ok := n.Tun.(interface{ Events() <-chan tun.Event })
	if !ok {
		return
	}
	n.tunWorkers.Add(1)
	go func() {
		defer n.tunWorkers.Done()
		for event := range host.Events() {
			if event&tun.EventMTUUpdate != 0 {
				n.applyHostMTU()
			}
			if event&tun.EventUp != 0 {
				n.Log.Info("host interface is up", "name", n.Interface)
			}
			if event&tun.EventDown != 0 {
				n.Log.Warn("host interface is down", "name", n.Interface)
			}
		}
	}()
}

func (n *Nylon) applyHostMTU() {
	mtu, err := n.Tun.MTU()
	if err != nil || mtu <= 0 {
		n.Log.Error("failed to read host MTU", "mtu", mtu, "err", err)
		return
	}
	for _, transport := range n.Transports {
		transport.SetHostMTU(mtu)
	}
}

func (n *Nylon) deliverTUN(packets [][]byte) error {
	// Use Nylon-owned buffers for the headers written by the TUN.
	pooled := make([]*[]byte, len(packets))
	buffers := make([][]byte, len(packets))
	for i, packet := range packets {
		pooled[i] = getHostBuffer(tunHeadroom + len(packet))
		buffers[i] = *pooled[i]
		copy(buffers[i][tunHeadroom:], packet)
	}
	// Implementations disagree on the count they return: Linux reports bytes.
	_, err := n.Tun.Write(buffers, tunHeadroom)
	for _, buffer := range pooled {
		hostBuffers.Put(buffer)
	}
	return err
}

var hostBuffers sync.Pool

// getHostBuffer returns a pooled buffer of length size, with at least
// minHostBufferCap capacity. A pooled buffer that is too small is replaced.
func getHostBuffer(size int) *[]byte {
	buffer, _ := hostBuffers.Get().(*[]byte)
	if buffer == nil || cap(*buffer) < size {
		buffer = new([]byte)
		*buffer = make([]byte, size, max(size, minHostBufferCap))
	}
	*buffer = (*buffer)[:size]
	return buffer
}
