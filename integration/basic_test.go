//go:build integration

package integration

import (
	"github.com/encodeous/nylon/state"
	"go.uber.org/goleak"
	"net/netip"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	state.DBG_log_wireguard = true
	state.DBG_log_route_table = true
	state.DBG_log_route_changes = true
	//state.DBG_log_probe = true
	m.Run()
}

func TestStartStop(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh := &VirtualHarness{}
	vh.NewNode("node1", "10.0.0.1/32")
	vh.NewNode("node2", "10.0.0.2/32")
	vh.NewNode("node3", "10.0.0.3/32")
	vh.Central.Graph = []string{
		"node1, node2, node3",
	}
	errs := vh.Start()
	select {
	case <-time.After(1000 * time.Millisecond):
	case err := <-errs:
		t.Error(err)
	}
	vh.Stop()
}

func TestSimplePing(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh := &VirtualHarness{}
	a1 := "192.168.1.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.1.2:1234"
	vh.NewNode("b", "10.0.0.2/32")
	vh.Central.Graph = []string{
		"a, b",
	}
	vh.Endpoints = map[string]state.NodeId{
		a1: "a",
		b1: "b",
	}
	vh.AddLink(a1, b1)
	vh.AddLink(b1, a1)

	errs := vh.Start()

	vn := vh.Net
	cc := make(chan bool, 100)

	vn.SelfHandler = func(node state.NodeId, src, dst netip.Addr, data []byte) bool {
		if node == "b" && src.String() == "10.0.0.1" && dst.String() == "10.0.0.2" && data[0] == 111 {
			cc <- true
		}
		return true
	}

	go func() {
		for {
			select {
			case <-vh.Context.Done():
				return
			case <-time.After(100 * time.Millisecond):
				vn.Send("a", "10.0.0.1", "10.0.0.2", []byte{111}, 64)
			}
		}
	}()

	select {
	case <-cc:
		t.Log("Got ping!")
	case <-time.After(10 * time.Second):
		t.Error("Timed out waiting for ping")
	case err := <-errs:
		t.Error(err)
	}
	vh.Stop()
}

func TestSimpleRoutedPing(t *testing.T) {
	defer goleak.VerifyNone(t)
	state.ProbeDelay /= 3 // 3x faster
	state.RouteUpdateDelay /= 3

	vh := &VirtualHarness{}
	a1 := "192.168.1.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.1.2:1234"
	vh.NewNode("b", "10.0.0.2/32")
	c1 := "192.168.1.3:1234"
	vh.NewNode("c", "10.0.0.3/32")
	vh.Central.Graph = []string{
		"a, b",
		"b, c",
	}
	vh.Endpoints = map[string]state.NodeId{
		a1: "a",
		b1: "b",
		c1: "c",
	}
	vh.AddLink(a1, b1)
	vh.AddLink(b1, a1)
	vh.AddLink(b1, c1)
	vh.AddLink(c1, b1)

	errs := vh.Start()

	vn := vh.Net
	cc := make(chan bool, 100)

	vn.SelfHandler = func(node state.NodeId, src, dst netip.Addr, data []byte) bool {
		if node == "c" && src.String() == "10.0.0.1" && dst.String() == "10.0.0.3" && data[0] == 222 {
			cc <- true
		}
		return true
	}

	go func() {
		for {
			select {
			case <-vh.Context.Done():
				return
			case <-time.After(100 * time.Millisecond):
				vn.Send("a", "10.0.0.1", "10.0.0.3", []byte{222}, 64)
			}
		}
	}()

	select {
	case <-cc:
		t.Log("Got ping!")
	case <-time.After(100 * time.Second):
		t.Error("Timed out waiting for ping")
	case err := <-errs:
		t.Error(err)
	}
	vh.Stop()
}
