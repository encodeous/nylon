//go:build integration

package integration

import (
	"fmt"
	"log/slog"
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"go.uber.org/goleak"
)

func TestRapidToggleConfig(t *testing.T) {
	defer goleak.VerifyNone(t)

	vh := &VirtualHarness{}
	vh.LogLevel = new(slog.LevelInfo)
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
	vn.SelfHandler = func(node state.NodeId, src, dst netip.Addr, data []byte) bool {
		return true
	}

	// Wait for initial convergence.
	time.Sleep(3 * time.Second)

	_, baseCfg := vh.Central.Clone()
	_, extraCfg := vh.Central.Clone()

	aIdx := vh.IndexOf("a")
	extraRouter := extraCfg.Routers[aIdx]
	for i := 0; i < 50; i++ {
		extraRouter.Prefixes = append(extraRouter.Prefixes, state.PrefixHealthWrapper{
			PrefixHealth: &state.StaticPrefixHealth{
				Prefix: netip.MustParsePrefix(fmt.Sprintf("10.1.0.%d/32", i)),
				Metric: 0,
			},
		})
	}
	extraCfg.Routers[aIdx] = extraRouter

	a := vh.Nylons[vh.IndexOf("a")].Load()

	// Break shared *DynamicEndpoint pointers from startup.
	{
		done := make(chan struct{})
		a.Dispatch(func() error {
			_, nc := a.CentralCfg.Clone()
			a.CentralCfg = *nc
			close(done)
			return nil
		})
		<-done
	}

	// Send packets every 10ms to trigger ForwardTable.Lookup via TC filter.
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-vh.Context.Done():
				return
			case <-ticker.C:
				vn.Send("a", "10.0.0.1", "10.0.0.2", []byte{1}, 64)
			}
		}
	}()

	// Rapidly toggle config until test duration expires.
	deadline := time.After(5 * time.Second)
	for i := 0; ; i++ {
		cfg := extraCfg
		if i%2 == 1 {
			cfg = baseCfg
		}
		cfg.Timestamp = baseCfg.Timestamp + int64(i) + 2
		done := make(chan struct{})
		_, ccfg := cfg.Clone()
		a.Dispatch(func() error {
			defer close(done)
			_, err := a.ApplyCentralConfig(ccfg)
			if err != nil {
				return err
			}
			return nil
		})
		select {
		case <-done:
			time.Sleep(10 * time.Millisecond)
		case <-deadline:
			goto end
		case err := <-errs:
			t.Fatalf("harness error: %v", err)
		}
	}
end:
	close(stop)
	vh.Stop()
}
