//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
)

func TestConnectivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	t.Parallel()

	// Use a specific subnet for this test to avoid conflicts
	subnet := "172.30.0.0/24"
	gateway := "172.30.0.1"

	h := NewHarness(t, subnet, gateway)

	// Generate keys
	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()
	node3Key := state.GenerateKey()

	// IPs in the docker network
	node1IP := "172.30.0.10"
	node2IP := "172.30.0.11"
	node3IP := "172.30.0.12"

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

	// 4. Start Containers in Parallel
	h.StartNodes(
		NodeSpec{Name: "node1", IP: node1IP, CentralConfigPath: centralPath, NodeConfigPath: node1Path},
		NodeSpec{Name: "node2", IP: node2IP, CentralConfigPath: centralPath, NodeConfigPath: node2Path},
		NodeSpec{Name: "node3", IP: node3IP, CentralConfigPath: centralPath, NodeConfigPath: node3Path},
	)

	// 5. Wait for convergence
	t.Log("Waiting for convergence...")
	h.WaitForLog("node3", "installing new route prefix=10.0.0.1/32")
	h.WaitForLog("node1", "installing new route prefix=10.0.0.2/31")

	// 6. Test Connectivity
	// Ping from node1 to node2's Nylon IP
	t.Logf("Pinging %s from node1...", node2NylonIP)
	stdout, stderr, err := h.Exec("node3", []string{"ping", "-c", "3", node1NylonIP})
	if err != nil {
		h.PrintLogs("node1")
		h.PrintLogs("node2")
		h.PrintLogs("node3")
		t.Fatalf("Ping failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	t.Logf("Ping output:\n%s", stdout)
}
