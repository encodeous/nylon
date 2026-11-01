//go:build e2e

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

func TestConnectivity(t *testing.T) {
	t.Parallel()
	// Use a specific subnet for this test to avoid conflicts
	h := NewHarness(t)

	// Generate keys
	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()
	node3Key := state.GenerateKey()

	// IPs in the docker network
	node1IP := GetIP(h.Subnet, 10)
	node2IP := GetIP(h.Subnet, 11)
	node3IP := GetIP(h.Subnet, 12)

	// Internal Nylon IPs
	node1NylonIP := "10.0.0.1"
	node2NylonIP := "10.0.0.2"
	node3NylonIP := "10.0.0.3"

	// Create config directory for this test run
	configDir := h.SetupTestDir()

	// 1. Create Central Config
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("node1", node1Key.Pubkey(), node1NylonIP, ""),
			SimpleRouter("node2", node2Key.Pubkey(), node2NylonIP, node2IP),
			SimpleRouter("node3", node3Key.Pubkey(), node3NylonIP, ""),
		},
		Graph: []string{
			"node1, node2",
			"node2, node3",
		},
		Timestamp: time.Now().UnixNano(),
	}

	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	// 2. Create Node Configs
	node1Cfg := SimpleLocal("node1", node1Key)
	node1Path := h.WriteConfig(configDir, "node1.yaml", node1Cfg)

	node2Cfg := SimpleLocal("node2", node2Key)
	node2Path := h.WriteConfig(configDir, "node2.yaml", node2Cfg)

	node3Cfg := SimpleLocal("node3", node3Key)
	node3Path := h.WriteConfig(configDir, "node3.yaml", node3Cfg)

	// The scenario is identical for Linux, Windows, and a Windows relay between
	// Linux endpoints. Only the deployment placement changes.
	specs := []NodeSpec{
		{Name: "node1", IP: node1IP, CentralConfigPath: centralPath, NodeConfigPath: node1Path},
		{Name: "node2", IP: node2IP, CentralConfigPath: centralPath, NodeConfigPath: node2Path},
		{Name: "node3", IP: node3IP, CentralConfigPath: centralPath, NodeConfigPath: node3Path},
	}
	if os.Getenv("NYLON_E2E_MACHINE") == "mixed" {
		specs[0].OS, specs[1].OS, specs[2].OS = machine.Linux, machine.Windows, machine.Linux
	}
	h.StartMachines(specs...)

	// 5. Wait for convergence
	t.Log("Waiting for convergence...")
	h.WaitForStatus(t, "node3", func(s *protocol.StatusResponse) bool {
		return HasSelectedRoute(s, "10.0.0.1/32", "node2", "node1")
	})
	h.WaitForStatus(t, "node1", func(s *protocol.StatusResponse) bool {
		return HasSelectedRoute(s, "10.0.0.3/32", "node2", "node3")
	})
	// Selected control-plane routes can precede system-route installation.
	// Wait for the data plane before asserting consecutive successful replies.
	deadline := time.Now().Add(WaitTimeout)
	for {
		result, err := h.Ping("node3", netip.MustParseAddr(node1NylonIP), machine.PingOptions{Count: 1})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("data plane did not converge: %v\n%s\n%s", err, result.Stdout, result.Stderr)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Check both multihop directions and a Windows-originated ping in mixed mode.
	for _, peer := range []struct{ from, to string }{
		{"node3", node1NylonIP}, {"node2", node1NylonIP}, {"node1", node3NylonIP},
	} {
		t.Logf("Pinging %s from %s...", peer.to, peer.from)
		result, err := h.Ping(peer.from, netip.MustParseAddr(peer.to), machine.PingOptions{Count: 3})
		if err != nil {
			for _, node := range []string{"node1", "node2", "node3"} {
				h.PrintLogs(node)
			}
			t.Fatalf("%s ping to %s: %v\n%s\n%s", peer.from, peer.to, err, result.Stdout, result.Stderr)
		}
		t.Logf("Ping output:\n%s", result.Stdout)
	}
}
