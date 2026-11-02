//go:build e2e && linux

package e2e

import (
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/encodeous/nylon/e2e/machine"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
)

// TestMachines checks Nylon startup, IPC, ping and process shutdown on two peers.
func TestMachines(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	dir := h.SetupTestDir()
	first, second := state.GenerateKey(), state.GenerateKey()
	firstIP, secondIP := GetIP(h.Subnet, 10), GetIP(h.Subnet, 11)
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("first", first.Pubkey(), "10.0.0.1", firstIP),
			SimpleRouter("second", second.Pubkey(), "10.0.0.2", secondIP),
		},
		Graph:     []string{"first, second"},
		Timestamp: time.Now().UnixNano(),
	}
	centralPath := h.WriteConfig(dir, "central.yaml", central)
	specs := []NodeSpec{
		{Name: "first", IP: firstIP, CentralConfigPath: centralPath, NodeConfigPath: h.WriteConfig(dir, "first.yaml", SimpleLocal("first", first))},
		{Name: "second", IP: secondIP, CentralConfigPath: centralPath, NodeConfigPath: h.WriteConfig(dir, "second.yaml", SimpleLocal("second", second))},
	}
	if os.Getenv("NYLON_E2E_MACHINE") == "mixed" {
		specs[0].OS, specs[1].OS = machine.Linux, machine.Windows
	}
	h.StartMachines(specs...)
	for _, peer := range []struct{ name, destination, nextHop string }{
		{"first", "10.0.0.2", "second"}, {"second", "10.0.0.1", "first"},
	} {
		h.WaitForStatus(t, peer.name, func(status *protocol.StatusResponse) bool {
			return HasSelectedRoute(status, peer.destination+"/32", peer.nextHop, peer.nextHop)
		})
		result, err := h.Ping(peer.name, netip.MustParseAddr(peer.destination), 3)
		if err != nil {
			h.PrintLogs(peer.name)
			t.Fatalf("%s ping: %v\n%s\n%s", peer.name, err, result.Stdout, result.Stderr)
		}
	}
	for name, p := range h.processes {
		if err := p.Stop(h.ctx); err != nil {
			t.Fatalf("stop %s: %v", name, err)
		}
		deadline := time.Now().Add(15 * time.Second)
		for {
			_, exited, err := p.Poll(h.ctx)
			if err != nil {
				t.Fatalf("poll stopped %s: %v", name, err)
			}
			if exited {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not stop", name)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}
