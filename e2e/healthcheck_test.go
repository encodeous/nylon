//go:build e2e

package e2e

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

func TestHealthcheckPing(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
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
	h.WaitForMatch("node3", ".+old.router=node2.+old.prefix=10.0.1.4\\/32.+new.router=node1.+new.prefix=10.0.1.4\\/32.+")
	h.WaitForLog("node1", "prefix=10.0.0.3/32")

	// ping from 3 to 10.0.0.4
	stdout, stderr, err := h.Exec("node3", []string{"ping", "-c", "3", "10.0.1.4"})
	if err != nil {
		t.Fatalf("Ping failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	t.Logf("Ping output:\n%s", stdout)

	msg := "hello from node 3"
	// listen on node 1
	bg := h.ExecBackground("node1", []string{"nc", "-l", "8888"})
	// send on node 3
	stdout, stderr, err = h.Exec("node3", []string{"bash", "-c", fmt.Sprintf("echo '%s' | nc -N 10.0.1.4 8888", msg)})
	if err != nil {
		t.Fatalf("Failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	//time.Sleep(time.Hour)
	stdout, stderr, err = bg.Wait()
	if err != nil {
		t.Fatalf("Failed to listen: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	assert.Equal(t, msg, strings.TrimSpace(stdout))
}

func TestHealthcheckHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	t.Parallel()

	h := NewHarness(t)

	// IPs
	clientIP := GetIP(h.Subnet, 20)
	primaryIP := GetIP(h.Subnet, 21)
	backupIP := GetIP(h.Subnet, 22)

	// Keys
	clientKey := state.GenerateKey()
	primaryKey := state.GenerateKey()
	backupKey := state.GenerateKey()

	configDir := h.SetupTestDir()

	// Service IP that we are load balancing / checking
	serviceIP := "10.0.3.1"
	servicePrefixStr := serviceIP + "/32"
	servicePrefix := netip.MustParsePrefix(servicePrefixStr)

	// 1. Central Config
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("client", clientKey.Pubkey(), "10.0.0.10", clientIP),
			SimpleRouter("primary", primaryKey.Pubkey(), "10.0.0.11", primaryIP),
			SimpleRouter("backup", backupKey.Pubkey(), "10.0.0.12", backupIP),
		},
		Graph: []string{
			"client, primary",
			"client, backup",
		},
		Timestamp: time.Now().UnixNano(),
	}

	// Configure Primary with HTTP check (Metric 10)
	// primMetric := uint32(10)
	checkDelay := 1 * time.Second
	central.Routers[1].Prefixes = []state.PrefixHealthWrapper{
		{
			&state.HTTPPrefixHealth{
				Prefix: servicePrefix,
				URL:    fmt.Sprintf("http://%s:8080/health", serviceIP),
				Delay:  &checkDelay,
				// Metric: &primMetric, // Remove override to use dynamic metric (RTT or INF)
			},
		},
	}

	// Configure Backup with Static check (Metric 1000)
	backupMetric := uint32(1000)
	central.Routers[2].Prefixes = []state.PrefixHealthWrapper{
		{
			&state.StaticPrefixHealth{
				Prefix: servicePrefix,
				Metric: backupMetric,
			},
		},
	}

	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	// 2. Local Configs
	clientCfg := SimpleLocal("client", clientKey)

	primaryCfg := SimpleLocal("primary", primaryKey)
	primaryCfg.PreUp = append(primaryCfg.PreUp, fmt.Sprintf("ip addr add %s dev lo", servicePrefixStr))

	backupCfg := SimpleLocal("backup", backupKey)
	backupCfg.PreUp = append(backupCfg.PreUp, fmt.Sprintf("ip addr add %s dev lo", servicePrefixStr))

	// Write configs
	h.WriteConfig(configDir, "client.yaml", clientCfg)
	h.WriteConfig(configDir, "primary.yaml", primaryCfg)
	h.WriteConfig(configDir, "backup.yaml", backupCfg)

	// 3. Start Nodes
	h.StartNodes(
		NodeSpec{Name: "client", IP: clientIP, CentralConfigPath: centralPath, NodeConfigPath: filepath.Join(configDir, "client.yaml")},
		NodeSpec{Name: "primary", IP: primaryIP, CentralConfigPath: centralPath, NodeConfigPath: filepath.Join(configDir, "primary.yaml")},
		NodeSpec{Name: "backup", IP: backupIP, CentralConfigPath: centralPath, NodeConfigPath: filepath.Join(configDir, "backup.yaml")},
	)

	// 4. Verification

	// A. Initial state: HTTP server is DOWN on primary.
	// Primary health check should fail (Metric INF).
	// Client should route to Backup (Metric 1000).

	t.Log("Step A: Waiting for routing to fallback (Primary DOWN)")
	h.WaitForMatch("client", "prefix=10\\.0\\.3\\.1/32.+new.nh=backup")

	// B. Start HTTP Server on Primary
	t.Log("Step B: Starting HTTP server on Primary")
	// Use python3 http.server. Create 'health' file so /health returns 200.
	// exec replaces the shell, so pkill python3 works or just killing the container process.
	serverCmd := `touch health && python3 -m http.server 8080`
	bg := h.ExecBackground("primary", []string{"/bin/sh", "-c", serverCmd})

	// C. Wait for Primary to become healthy
	// Primary should advertise Metric 10.
	// Client should switch to Primary.
	t.Log("Step C: Waiting for routing to switch to Primary (Primary UP)")
	h.WaitForMatch("client", "prefix=10\\.0\\.3\\.1/32.+new.nh=primary")

	// D. Stop HTTP Server
	t.Log("Step D: Stopping HTTP server")
	h.Exec("primary", []string{"pkill", "python3"})
	bg.Wait()

	// E. Wait for fallback to Backup

	time.Sleep(5 * time.Second)

	h.mu.Lock()
	logs := h.LogBuffers["client"].String()
	h.mu.Unlock()

	idxPrimary := strings.LastIndex(logs, "new.nh=primary")
	idxBackup := strings.LastIndex(logs, "new.nh=backup")

	if idxBackup > idxPrimary {
		t.Log("Verified: Route switched back to backup")
	} else {
		t.Fatalf("Route failed to switch back to backup. Logs:\n%s", logs)
	}
}
