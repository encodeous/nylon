//go:build integration

package integration

import (
	"github.com/encodeous/nylon/state"
	"go.uber.org/goleak"
	"net/netip"
	"testing"
	"time"
)

func TestOptimalConvergence(t *testing.T) {
	defer goleak.VerifyNone(t)

	vh := &VirtualHarness{}
	a1 := "192.168.1.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.1.2:1234"
	vh.NewNode("b", "10.0.0.2/32")
	c1 := "192.168.1.3:1234"
	vh.NewNode("c", "10.0.0.3/32")
	vh.Central.Graph = []string{
		"a, b, c",
	}
	vh.Endpoints = map[string]state.NodeId{
		a1: "a",
		b1: "b",
		c1: "c",
	}
	// a <-10-> b
	vh.AddLink(a1, b1).WithLatency(10*time.Millisecond, 0)
	vh.AddLink(b1, a1).WithLatency(10*time.Millisecond, 0)

	// c <-50-> a
	vh.AddLink(a1, c1).WithLatency(50*time.Millisecond, 0)
	vh.AddLink(c1, a1).WithLatency(50*time.Millisecond, 0)

	errs := vh.Start()

	vn := vh.Net

	conv1 := NewSignal() // first stage convergence: c <-50-> a <-10-> b
	conv2 := NewSignal() // second stage convergence: a <-10-> b <-10-> c
	success := NewSignal()

	go func() {
		conv1.Wait()

		// b <-10-> c
		vh.AddLink(b1, c1).WithLatency(10*time.Millisecond, 0)
		vh.AddLink(c1, b1).WithLatency(10*time.Millisecond, 0)

	}()

	vn.TransitHandler = func(node state.NodeId, src, dst netip.Addr, data []byte) bool {
		if node == "b" && src.String() == "10.0.0.1" && dst.String() == "10.0.0.3" && data[0] == 222 && conv1.Triggered() {
			// this means the network has reached the optimal path
			conv2.Trigger()
		}
		return false // don't intercept the packet
	}
	vn.SelfHandler = func(node state.NodeId, src, dst netip.Addr, data []byte) bool {
		if node == "c" && src.String() == "10.0.0.1" && dst.String() == "10.0.0.3" && data[0] == 222 {
			conv1.Trigger()
			if conv2.Triggered() {
				success.Trigger()
			}
		}
		return true
	}

	go func() {
		for {
			select {
			case <-vh.Context.Done():
				return
			case <-time.After(100 * time.Millisecond):
				vn.Send("a", "10.0.0.1", "10.0.0.3", []byte{222})
			}
		}
	}()

	select {
	case <-success:
		t.Log("Reached optimal!")
	case <-time.After(100 * time.Second):
		t.Error("Timed out waiting for ping")
	case err := <-errs:
		t.Error(err)
	}
	vh.Stop()
}
