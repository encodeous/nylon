//go:build integration

package integration

import (
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/encodeous/nylon/state"
)

// lineTopology builds a - b - c with each node on its own endpoint.
func lineTopology(h *Harness) {
	a1, b1, c1 := "192.168.1.1:1234", "192.168.1.2:1234", "192.168.1.3:1234"
	h.AddNode("a", "10.0.0.1/32", a1)
	h.AddNode("b", "10.0.0.2/32", b1)
	h.AddNode("c", "10.0.0.3/32", c1)
	h.Central.Graph = []string{"a, b", "b, c"}
	h.Connect(a1, b1)
	h.Connect(b1, c1)
}

func TestInProcessRouting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		h.UntrackedRouting = true
		lineTopology(h)

		got := expectPacket(h, "c", "10.0.0.1", "10.0.0.3", 222)
		h.Start()
		h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.3", []byte{222}, 64)
		await(h, got, 10*time.Second, "ping from a to c")
	})
}

func TestInProcessRoutingThroughTUNLessRelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		h.UntrackedRouting = true
		lineTopology(h)
		h.LocalCfg("b").NoTun = true

		got := expectPacket(h, "c", "10.0.0.1", "10.0.0.3", 222)
		h.Start()
		h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.3", []byte{222}, 64)
		await(h, got, 10*time.Second, "packet through TUN-less relay")
	})
}

func TestTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		h.UntrackedRouting = true
		lineTopology(h)

		src, dst := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.3")
		expired := NewSignal()
		h.OnDeliver(func(node state.NodeId, s, d netip.Addr, data []byte) bool {
			if s == src && d == dst && data[0] == 222 {
				if node == "c" {
					t.Error("c received a packet whose TTL should have expired at b")
				}
				expired.Trigger()
			}
			return true
		})
		h.Start()
		h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.3", []byte{222}, 1)
		await(h, expired, 10*time.Second, "packet with expired TTL")
	})
}
