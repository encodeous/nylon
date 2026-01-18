//go:build e2e

package e2e

import (
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
)

func TestHealthcheckPing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	t.Parallel()

	// Use a specific subnet for this test to avoid conflicts
	subnet := "172.30.1.0/24"
	gateway := "172.30.1.1"

	h := NewHarness(t, subnet, gateway)

	// Generate keys
	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()
	node3Key := state.GenerateKey()

	// IPs in the docker network
	node1IP := "172.30.1.10"
	node2IP := "172.30.1.11"
	node3IP := "172.30.1.12"

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

	// make node 1 and node 2 both advertise 10.0.0.4/32
	// 1 would be default
	n1Metric := uint32(10)
	central.Routers[0].Prefixes = []state.PrefixHealthWrapper{
		{
			&state.PingPrefixHealth{
				Prefix: netip.MustParsePrefix("10.0.1.4/32"),
				Addr:   netip.MustParseAddr("10.0.1.4"),
				Metric: &n1Metric,
			},
		},
	}
	// 2 would be fallback
	n2Metric := uint32(1000)
	central.Routers[1].Prefixes = []state.PrefixHealthWrapper{
		{
			&state.PingPrefixHealth{
				Prefix: netip.MustParsePrefix("10.0.1.4/32"),
				Addr:   netip.MustParseAddr("10.0.1.4"),
				Metric: &n2Metric,
			},
		},
	}

	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	// 2. Create Node Configs
	node1Cfg := SimpleLocal("node1", node1Key)
	node2Cfg := SimpleLocal("node2", node2Key)
	node3Cfg := SimpleLocal("node3", node3Key)

	// add a dummy loopback interface on each node
	node1Cfg.PreUp = append(node1Cfg.PreUp, "ip addr add 10.0.1.4/32 dev lo")
	node1Cfg.PreUp = append(node1Cfg.PreUp, "ip route add 10.0.1.4/32 dev lo")
	node2Cfg.PreUp = append(node2Cfg.PreUp, "ip addr add 10.0.1.4/32 dev lo")
	node2Cfg.PreUp = append(node2Cfg.PreUp, "ip route add 10.0.1.4/32 dev lo")

	node1Path := h.WriteConfig(configDir, "node1.yaml", node1Cfg)
	node2Path := h.WriteConfig(configDir, "node2.yaml", node2Cfg)
	node3Path := h.WriteConfig(configDir, "node3.yaml", node3Cfg)

	// 4. Start Containers in Parallel
	h.StartNodes(
		NodeSpec{Name: "node1", IP: node1IP, CentralConfigPath: centralPath, NodeConfigPath: node1Path},
		NodeSpec{Name: "node2", IP: node2IP, CentralConfigPath: centralPath, NodeConfigPath: node2Path},
		NodeSpec{Name: "node3", IP: node3IP, CentralConfigPath: centralPath, NodeConfigPath: node3Path},
	)

	// 5. Wait for convergence
	t.Log("Waiting for convergence...")
	h.WaitForLog("node3", "installing new route prefix=10.0.1.4/32")
	h.WaitForLog("node1", "installing new route prefix=10.0.0")

	// ping from 3 to 10.0.0.4
	stdout, stderr, err := h.Exec("node3", []string{"ping", "-c", "3", "10.0.1.4"})
	if err != nil {
		t.Fatalf("Ping failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	t.Logf("Ping output:\n%s", stdout)

	// listen on node 1
	stdout, stderr, err = h.Exec("node1", []string{"bash", "-c", "nc -l -p 8888 &"})
	if err != nil {
		t.Fatalf("Failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	// send on node 3
	stdout, stderr, err = h.Exec("node3", []string{"bash", "-c", "echo 'hello from node 3'"})
	if err != nil {
		t.Fatalf("Failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	h.WaitForLog("node1", "hello from node 3")
}
