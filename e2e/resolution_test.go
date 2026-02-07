//go:build e2e

package e2e

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
)

func TestEndpointResolution(t *testing.T) {
	h := NewHarness(t)

	dnsIP := GetIP(h.Subnet, 100)
	node1IP := GetIP(h.Subnet, 2)
	node2IP := GetIP(h.Subnet, 3)

	// example.com -> node1IP
	// srv.example.com -> SRV _nylon._udp.srv.example.com -> 57175 node2.example.com
	// node2.example.com -> node2IP
	corefile := `
. {
    file /etc/coredns/example.com.db example.com
    log
    errors
}
`
	zoneFile := fmt.Sprintf(`
example.com. 0 IN SOA sns.dns.icann.org. noc.dns.icann.org. 2017042745 7200 3600 1209600 0
example.com. 0 IN A %s
node2.example.com. 0 IN A %s
_nylon._udp.srv.example.com. 0 IN SRV 10 10 57175 node2.example.com.
`, node1IP, node2IP)
	h.StartDNS("dns", dnsIP, corefile, map[string]string{"example.com.db": zoneFile})

	key1 := state.GenerateKey()
	key2 := state.GenerateKey()

	centralCfg := state.CentralCfg{
		Routers: []state.RouterCfg{
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-1",
					PubKey:    key1.Pubkey(),
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
				},
				// Node 1's endpoint is a hostname
				Endpoints: []*state.DynamicEndpoint{
					state.NewDynamicEndpoint("example.com"),
				},
			},
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-2",
					PubKey:    key2.Pubkey(),
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.2")},
				},
				// Node 2's endpoint is an SRV record
				Endpoints: []*state.DynamicEndpoint{
					state.NewDynamicEndpoint("srv.example.com"),
				},
			},
		},
		Graph: []string{"node-1, node-2"},
	}

	testDir := h.SetupTestDir()
	centralPath := h.WriteConfig(testDir, "central.yaml", centralCfg)

	node1Cfg := SimpleLocal("node-1", key1)
	node1Cfg.DnsResolvers = []string{dnsIP + ":53"}
	node1Path := h.WriteConfig(testDir, "node1.yaml", node1Cfg)

	node2Cfg := SimpleLocal("node-2", key2)
	node2Cfg.DnsResolvers = []string{dnsIP + ":53"}
	node2Path := h.WriteConfig(testDir, "node2.yaml", node2Cfg)

	h.StartNode("node-1", node1IP, centralPath, node1Path)
	h.StartNode("node-2", node2IP, centralPath, node2Path)

	h.WaitForLog("node-1", "Nylon has been initialized")
	h.WaitForLog("node-2", "Nylon has been initialized")

	verify := func(node string, expectedPattern string) {
		timeout := time.After(30 * time.Second)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-timeout:
				t.Fatalf("timed out waiting for resolution pattern %q on node %s", expectedPattern, node)
			case <-ticker.C:
				stdout, _, err := h.Exec(node, []string{"nylon", "inspect", "nylon0"})
				if err != nil {
					continue
				}
				if strings.Contains(stdout, expectedPattern) {
					return
				}
			}
		}
	}

	// node-1 should resolve node-2 (srv.example.com) to node2IP:57175
	verify("node-1", fmt.Sprintf("srv.example.com (resolved: %s:57175)", node2IP))

	// node-2 should resolve node-1 (example.com) to node1IP:57175
	verify("node-2", fmt.Sprintf("example.com (resolved: %s:%d)", node1IP, state.DefaultPort))
}

func TestDynamicResolution(t *testing.T) {
	h := NewHarness(t)

	dnsIP := GetIP(h.Subnet, 100)
	node1IP := GetIP(h.Subnet, 2)
	node2IP_A := GetIP(h.Subnet, 3)
	node2IP_B := GetIP(h.Subnet, 4)

	// Initial DNS setup
	corefile := `
. {
    file /etc/coredns/example.com.db example.com {
		reload 2s
	}
    log
    errors
}
`
	zoneFileA := fmt.Sprintf(`
example.com. 0 IN SOA sns.dns.icann.org. noc.dns.icann.org. 2017042745 7200 3600 1209600 0
node2.example.com. 0 IN A %s
`, node2IP_A)
	h.StartDNS("dns", dnsIP, corefile, map[string]string{"example.com.db": zoneFileA})

	key1 := state.GenerateKey()
	key2 := state.GenerateKey()

	centralCfg := state.CentralCfg{
		Routers: []state.RouterCfg{
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-1",
					PubKey:    key1.Pubkey(),
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
				},
			},
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-2",
					PubKey:    key2.Pubkey(),
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.2")},
				},
				// Node 2's endpoint is a hostname
				Endpoints: []*state.DynamicEndpoint{
					state.NewDynamicEndpoint("node2.example.com"),
				},
			},
		},
		Graph: []string{"node-1, node-2"},
	}

	testDir := h.SetupTestDir()
	centralPath := h.WriteConfig(testDir, "central.yaml", centralCfg)

	node1Cfg := SimpleLocal("node-1", key1)
	node1Cfg.DnsResolvers = []string{dnsIP + ":53"}
	node1Path := h.WriteConfig(testDir, "node1.yaml", node1Cfg)

	node2Cfg := SimpleLocal("node-2", key2)
	node2Cfg.DnsResolvers = []string{dnsIP + ":53"}
	node2Path := h.WriteConfig(testDir, "node2.yaml", node2Cfg)

	h.StartNode("node-1", node1IP, centralPath, node1Path)
	h.StartNode("node-2", node2IP_A, centralPath, node2Path)

	h.WaitForLog("node-1", "Nylon has been initialized")
	h.WaitForLog("node-2", "Nylon has been initialized")

	verify := func(node string, expectedPattern string) {
		timeout := time.After(60 * time.Second)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-timeout:
				// Print logs of node-1 to help debugging
				h.PrintLogs("node-1")
				stdout, _, _ := h.Exec(node, []string{"nylon", "inspect", "nylon0"})
				t.Fatalf("timed out waiting for resolution pattern %q on node %s. Current inspect:\n%s", expectedPattern, node, stdout)
			case <-ticker.C:
				stdout, _, err := h.Exec(node, []string{"nylon", "inspect", "nylon0"})
				if err != nil {
					continue
				}
				if strings.Contains(stdout, expectedPattern) {
					return
				}
			}
		}
	}

	// Verify initial connection
	verify("node-1", fmt.Sprintf("node2.example.com (resolved: %s:57175)", node2IP_A))

	h.WaitForLog("node-1", "installing new route prefix=10.0.0.2")
	h.WaitForLog("node-2", "installing new route prefix=10.0.0.1")

	// Ping from node-1 to node-2
	_, _, err := h.Exec("node-1", []string{"ping", "-c", "3", "10.0.0.2"})
	if err != nil {
		t.Fatalf("initial ping failed: %v", err)
	}

	// Update DNS record to point to node2IP_B
	zoneFileB := fmt.Sprintf(`
example.com. 0 IN SOA sns.dns.icann.org. noc.dns.icann.org. 2017042746 7200 3600 1209600 0
node2.example.com. 0 IN A %s
`, node2IP_B)

	zonePath := filepath.Join(testDir, "example.com.db.new")
	if err := os.WriteFile(zonePath, []byte(zoneFileB), 0644); err != nil {
		t.Fatalf("failed to write new zone file: %v", err)
	}
	h.CopyFile("dns", zonePath, "/etc/coredns/example.com.db")

	// Stop old node-2
	h.mu.Lock()
	node2Container := h.Nodes["node-2"]
	h.mu.Unlock()
	err = node2Container.Terminate(h.ctx)
	if err != nil {
		t.Logf("failed to terminate node-2: %v", err)
	}

	// Start new node-2 at node2IP_B
	h.StartNode("node-2-new", node2IP_B, centralPath, node2Path)
	h.WaitForLog("node-2-new", "Nylon has been initialized")

	// Wait for Nylon on node-1 to re-resolve.
	verify("node-1", fmt.Sprintf("node2.example.com (resolved: %s:57175)", node2IP_B))

	h.WaitForLog("node-2-new", "installing new route prefix=10.0.0.1/32")

	// Ping from node-1 to node-2 (at new IP)
	var lastErr error
	for i := 0; i < 15; i++ {
		_, _, lastErr = h.Exec("node-1", []string{"ping", "-c", "1", "-W", "1", "10.0.0.2"})
		if lastErr == nil {
			break
		}
		t.Logf("Ping attempt %d failed: %v", i+1, lastErr)
		time.Sleep(2 * time.Second)
	}
	if lastErr != nil {
		stdout, _, _ := h.Exec("node-1", []string{"nylon", "inspect", "nylon0"})
		t.Logf("Node 1 inspect:\n%s", stdout)
		stdout2, _, _ := h.Exec("node-2-new", []string{"nylon", "inspect", "nylon0"})
		t.Logf("Node 2-new inspect:\n%s", stdout2)
		t.Fatalf("ping after DNS change failed after retries: %v", lastErr)
	}
}
