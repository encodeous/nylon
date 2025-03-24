//go:build integration

package integration

import (
	"github.com/encodeous/nylon/state"
	"net/netip"
	"testing"
	"time"
)

func TestInProcessRouting(t *testing.T) {
	vh := &VirtualHarness{}
	vh.UntrackedRouting = true
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
				vn.Send("a", "10.0.0.1", "10.0.0.3", []byte{222})
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
