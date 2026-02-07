//go:build e2e

package e2e

import (
	"fmt"
	"net/netip"
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
example.com. IN SOA sns.dns.icann.org. noc.dns.icann.org. 2017042745 7200 3600 1209600 3600
example.com. IN A %s
node2.example.com. IN A %s
_nylon._udp.srv.example.com. IN SRV 10 10 57175 node2.example.com.
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
					{Value: "example.com"},
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
					{Value: "srv.example.com"},
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
