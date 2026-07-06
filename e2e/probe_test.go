//go:build e2e

package e2e

import (
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestProbeReportsResolvedDNSAndLatency(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)

	dnsIP := GetIP(h.Subnet, 100)
	node1IP := GetIP(h.Subnet, 2)
	node2IP := GetIP(h.Subnet, 3)
	node2Endpoint := "node2.probe.test"

	startProbeDNS(h, dnsIP, fmt.Sprintf(`
probe.test. 0 IN SOA sns.dns.icann.org. noc.dns.icann.org. 2017042745 7200 3600 1209600 0
node2.probe.test. 0 IN A %s
`, node2IP))

	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()
	configDir := h.SetupTestDir()
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			probeRouter("node1", node1Key.Pubkey(), "10.0.0.1", ""),
			probeRouter("node2", node2Key.Pubkey(), "10.0.0.2", node2Endpoint),
		},
		Graph:     []string{"node1, node2"},
		Timestamp: time.Now().UnixNano(),
	}
	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	node1Cfg := SimpleLocal("node1", node1Key)
	node1Cfg.DnsResolvers = []string{dnsIP + ":53"}
	node1Path := h.WriteConfig(configDir, "node1.yaml", node1Cfg)
	node2Path := h.WriteConfig(configDir, "node2.yaml", SimpleLocal("node2", node2Key))

	h.StartNodes(
		NodeSpec{Name: "node1", IP: node1IP, CentralConfigPath: centralPath, NodeConfigPath: node1Path},
		NodeSpec{Name: "node2", IP: node2IP, CentralConfigPath: centralPath, NodeConfigPath: node2Path},
	)
	h.WaitForStatus(t, "node1", func(status *protocol.StatusResponse) bool {
		return HasResolvedEndpoint(status, node2Endpoint, fmt.Sprintf("%s:57175", node2IP))
	})

	result := waitForProbeResult(t, h, "node1", "node2", node2Endpoint, 2*time.Second, func(result *protocol.EndpointProbeResult) bool {
		return result.GetStatus() == protocol.EndpointProbeStatus_ENDPOINT_PROBE_REPLIED &&
			result.GetResolved() == fmt.Sprintf("%s:57175", node2IP) &&
			result.GetLatencyNs() > 0
	})
	t.Logf("probe latency: %s", time.Duration(result.GetLatencyNs()))
}

func TestProbeReportsResolveErrorForMissingDNS(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)

	dnsIP := GetIP(h.Subnet, 100)
	node1IP := GetIP(h.Subnet, 2)
	missingEndpoint := "missing.probe.test"

	startProbeDNS(h, dnsIP, `
probe.test. 0 IN SOA sns.dns.icann.org. noc.dns.icann.org. 2017042745 7200 3600 1209600 0
`)

	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()
	configDir := h.SetupTestDir()
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			probeRouter("node1", node1Key.Pubkey(), "10.0.0.1", ""),
			probeRouter("node2", node2Key.Pubkey(), "10.0.0.2", missingEndpoint),
		},
		Graph:     []string{"node1, node2"},
		Timestamp: time.Now().UnixNano(),
	}
	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	node1Cfg := SimpleLocal("node1", node1Key)
	node1Cfg.DnsResolvers = []string{dnsIP + ":53"}
	node1Path := h.WriteConfig(configDir, "node1.yaml", node1Cfg)

	h.StartNode("node1", node1IP, centralPath, node1Path)

	waitForProbeResult(t, h, "node1", "node2", missingEndpoint, 200*time.Millisecond, func(result *protocol.EndpointProbeResult) bool {
		return result.GetStatus() == protocol.EndpointProbeStatus_ENDPOINT_PROBE_RESOLVE_ERROR &&
			result.GetResolved() == "" &&
			result.GetLatencyNs() == 0
	})
}

func TestProbeReportsTimeout(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)

	node1IP := GetIP(h.Subnet, 2)
	unusedNode2IP := GetIP(h.Subnet, 3)
	node2Endpoint := fmt.Sprintf("%s:57175", unusedNode2IP)

	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()
	configDir := h.SetupTestDir()
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			probeRouter("node1", node1Key.Pubkey(), "10.0.0.1", ""),
			probeRouter("node2", node2Key.Pubkey(), "10.0.0.2", node2Endpoint),
		},
		Graph:     []string{"node1, node2"},
		Timestamp: time.Now().UnixNano(),
	}
	centralPath := h.WriteConfig(configDir, "central.yaml", central)
	node1Path := h.WriteConfig(configDir, "node1.yaml", SimpleLocal("node1", node1Key))

	h.StartNode("node1", node1IP, centralPath, node1Path)

	waitForProbeResult(t, h, "node1", "node2", node2Endpoint, 200*time.Millisecond, func(result *protocol.EndpointProbeResult) bool {
		return result.GetStatus() == protocol.EndpointProbeStatus_ENDPOINT_PROBE_TIMEOUT &&
			result.GetLatencyNs() == 0
	})
}

func startProbeDNS(h *Harness, dnsIP string, zoneFile string) {
	corefile := `
. {
    file /etc/coredns/probe.test.db probe.test
    log
    errors
}
`
	h.StartDNS("dns", dnsIP, corefile, map[string]string{"probe.test.db": zoneFile})
}

func probeRouter(id string, pubKey state.NyPublicKey, nylonIP string, endpoint string) state.RouterCfg {
	cfg := state.RouterCfg{
		NodeCfg: state.NodeCfg{
			Id:        state.NodeId(id),
			PubKey:    pubKey,
			Addresses: []netip.Addr{netip.MustParseAddr(nylonIP)},
		},
	}
	if endpoint != "" {
		cfg.Endpoints = []*state.DynamicEndpoint{state.NewDynamicEndpoint(endpoint)}
	}
	return cfg
}

func waitForProbeResult(t *testing.T, h *Harness, nodeName, peerName, address string, probeTimeout time.Duration, check func(*protocol.EndpointProbeResult) bool) *protocol.EndpointProbeResult {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	last := ""
	for time.Now().Before(deadline) {
		resp, stdout, stderr, err := runProbeJSON(h, nodeName, peerName, probeTimeout)
		if err == nil && resp.GetOk() {
			for _, result := range resp.GetProbe().GetResults() {
				if result.GetAddress() == address && check(result) {
					return result
				}
			}
			last = stdout
		} else if err != nil {
			last = fmt.Sprintf("error: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
		} else {
			last = fmt.Sprintf("probe response not ok: %s\nstdout:\n%s", resp.GetError(), stdout)
		}
		time.Sleep(500 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for probe result address=%q from %s to %s. Last result:\n%s", address, nodeName, peerName, last)
	return nil
}

func runProbeJSON(h *Harness, nodeName, peerName string, timeout time.Duration) (*protocol.IpcResponse, string, string, error) {
	stdout, stderr, err := h.Exec(nodeName, []string{
		"nylon",
		"probe",
		"-i",
		"nylon0",
		"--timeout",
		timeout.String(),
		"--json",
		peerName,
	})
	if err != nil {
		return nil, stdout, stderr, err
	}

	resp := &protocol.IpcResponse{}
	if err := protojson.Unmarshal([]byte(stdout), resp); err != nil {
		return nil, stdout, stderr, err
	}
	return resp, stdout, stderr, nil
}
