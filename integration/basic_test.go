//go:build integration

package integration

import (
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/encodeous/nylon/state"
)

func TestStartStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		h.AddNode("node1", "10.0.0.1/32")
		h.AddNode("node2", "10.0.0.2/32")
		h.AddNode("node3", "10.0.0.3/32")
		h.Central.Graph = []string{"node1, node2, node3"}
		h.Start()
		time.Sleep(time.Second)
	})
}

func TestSimplePing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		a1, b1 := "192.168.1.1:1234", "192.168.1.2:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.Central.Graph = []string{"a, b"}
		h.Connect(a1, b1)

		got := expectPacket(h, "b", "10.0.0.1", "10.0.0.2", 111)
		h.Start()
		h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.2", []byte{111}, 64)
		await(h, got, 10*time.Second, "ping from a to b")
	})
}

func TestSimpleRoutedPing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tunables := state.DefaultRouterTunables()
		tunables.ProbeDelay /= 3 // 3x faster
		tunables.RouteUpdateDelay /= 3

		h := NewHarness(t)
		h.Tunables = &tunables
		a1, b1, c1 := "192.168.1.1:1234", "192.168.1.2:1234", "192.168.1.3:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.AddNode("c", "10.0.0.3/32", c1)
		h.Central.Graph = []string{"a, b", "b, c"}
		h.Connect(a1, b1)
		h.Connect(b1, c1)

		got := expectPacket(h, "c", "10.0.0.1", "10.0.0.3", 222)
		h.Start()
		h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.3", []byte{222}, 64)
		await(h, got, 100*time.Second, "ping from a to c through b")
	})
}

// expectPacket returns a channel that receives once node has been delivered a
// packet from src to dst whose payload starts with marker. It replaces any
// OnDeliver filter.
func expectPacket(h *Harness, node state.NodeId, src, dst string, marker byte) <-chan struct{} {
	got := NewSignal()
	srcAddr, dstAddr := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	h.OnDeliver(func(at state.NodeId, s, d netip.Addr, data []byte) bool {
		if at == node && s == srcAddr && d == dstAddr && len(data) > 0 && data[0] == marker {
			got.Trigger()
		}
		return true
	})
	return got
}
