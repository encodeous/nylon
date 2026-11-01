//go:build integration

package integration

import (
	"fmt"
	"log/slog"
	"net/netip"
	"testing"
	"testing/synctest"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/state"
)

// TestRapidToggleConfig flips a's config between two versions while packets
// flow, so config applies race with ForwardTable lookups in the TC filter.
func TestRapidToggleConfig(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := NewHarness(t)
		h.LogLevel = slog.LevelInfo
		a1, b1 := "192.168.1.1:1234", "192.168.1.2:1234"
		h.AddNode("a", "10.0.0.1/32", a1)
		h.AddNode("b", "10.0.0.2/32", b1)
		h.Central.Graph = []string{"a, b"}
		h.Connect(a1, b1)
		h.OnDeliver(func(state.NodeId, netip.Addr, netip.Addr, []byte) bool { return true })
		h.Start()

		// Let the initial convergence happen.
		time.Sleep(3 * time.Second)

		_, baseCfg := h.Central.Clone()
		_, extraCfg := h.Central.Clone()
		extraRouter := routerOf(extraCfg, "a")
		for i := range 50 {
			extraRouter.Prefixes = append(extraRouter.Prefixes, state.PrefixHealthWrapper{
				PrefixHealth: &state.StaticPrefixHealth{
					Prefix: netip.MustParsePrefix(fmt.Sprintf("10.1.0.%d/32", i)),
				},
			})
		}

		h.SendEvery(10*time.Millisecond, "a", "10.0.0.1", "10.0.0.2", []byte{1}, 64)

		deadline := time.Now().Add(5 * time.Second)
		for i := 0; time.Now().Before(deadline); i++ {
			cfg := extraCfg
			if i%2 == 1 {
				cfg = baseCfg
			}
			cfg.Timestamp = baseCfg.Timestamp + int64(i) + 2
			_, next := cfg.Clone()
			err := OnNode(h, "a", func(n *core.Nylon) error {
				_, err := n.ApplyCentralConfig(next)
				return err
			})
			if err != nil {
				t.Fatalf("apply config #%d: %v", i, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
}
