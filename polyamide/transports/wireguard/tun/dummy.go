package tun

import (
	"os"
	"sync"
)

// DummyDevice is a userspace-only TUN device. It lets the WireGuard data plane
// operate without creating a host network interface. Reads block until the
// device is closed, while writes are discarded.
type DummyDevice struct {
	name      string
	closed    chan struct{}
	events    chan Event
	closeOnce sync.Once
}

func NewDummyDevice(name string) *DummyDevice {
	return &DummyDevice{
		name:   name,
		closed: make(chan struct{}),
		events: make(chan Event),
	}
}

func (d *DummyDevice) File() *os.File {
	return nil
}

func (d *DummyDevice) Read(_ [][]byte, _ []int, _ int) (int, error) {
	<-d.closed
	return 0, os.ErrClosed
}

func (d *DummyDevice) Write(bufs [][]byte, _ int) (int, error) {
	select {
	case <-d.closed:
		return 0, os.ErrClosed
	default:
		return len(bufs), nil
	}
}

func (d *DummyDevice) MTU() (int, error) {
	return 1420, nil
}

func (d *DummyDevice) Name() (string, error) {
	return d.name, nil
}

func (d *DummyDevice) Events() <-chan Event {
	return d.events
}

func (d *DummyDevice) Close() error {
	d.closeOnce.Do(func() {
		close(d.closed)
		close(d.events)
	})
	return nil
}

func (d *DummyDevice) BatchSize() int {
	return 1
}
