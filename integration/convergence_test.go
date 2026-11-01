//go:build integration

package integration

import (
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/encodeous/nylon/state"
)

// TestOptimalConvergence starts with a <-10-> b and a <-50-> c, so a reaches c
// directly. Once that works, it adds b <-10-> c and expects a to switch to the
// cheaper path through b.
func TestOptimalConvergence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		tunables := state.DefaultRouterTunables()
		tunables.ProbeDelay /= 3 // 3x faster
		tunables.RouteUpdateDelay /= 3
		tunables.MinimumConfidenceWindow /= 5

		h := NewHarness(t)
		h.Tunables = &tunables
		a1, b1, c1 := "192.168.1.1:1234", "192.168.1.2:1234", "192.168.1.3:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.AddNode("c", "10.0.0.3/32", c1)
		h.Central.Graph = []string{"a, b, c"}
		h.Connect(a1, b1).WithLatency(10*time.Millisecond, 0)
		h.Connect(a1, c1).WithLatency(50*time.Millisecond, 0)

		src, dst := netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("10.0.0.3")
		isProbe := func(s, d netip.Addr, data []byte) bool { return s == src && d == dst && data[0] == 222 }
		direct := NewSignal()  // a reached c over the direct link
		viaB := NewSignal()    // after that, a packet went through b
		optimal := NewSignal() // and reached c
		h.OnTransit(func(node state.NodeId, s, d netip.Addr, data []byte) bool {
			if node == "b" && isProbe(s, d, data) && direct.Triggered() {
				viaB.Trigger()
			}
			return false // route it on as usual
		})
		h.OnDeliver(func(node state.NodeId, s, d netip.Addr, data []byte) bool {
			if node == "c" && isProbe(s, d, data) {
				direct.Trigger()
				if viaB.Triggered() {
					optimal.Trigger()
				}
			}
			return true
		})

		h.Start()
		h.SendEvery(100*time.Millisecond, "a", "10.0.0.1", "10.0.0.3", []byte{222}, 64)

		await(h, direct, 200*time.Second, "a to reach c directly")
		h.Connect(b1, c1).WithLatency(10*time.Millisecond, 0)
		await(h, optimal, 200*time.Second, "a to route to c through b")
	})
}
