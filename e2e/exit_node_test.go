//go:build e2e

package e2e

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
)

// TestExplicitExitNodeMultiHop verifies the end-to-end path of an explicit
// exit-node packet: client encaps, relay transits without ever installing a
// default route, exit decaps and replies. It also validates that the
// downstream trace ("ExitEncap" / "ExitTransit" / "ExitDecap") fires the
// way the NyUnicast filter is supposed to log it.
func TestExplicitExitNodeMultiHop(t *testing.T) {
	h := NewHarness(t)

	clientKey := state.GenerateKey()
	relayKey := state.GenerateKey()
	exitKey := state.GenerateKey()

	clientIP := GetIP(h.Subnet, 10)
	relayIP := GetIP(h.Subnet, 11)
	exitIP := GetIP(h.Subnet, 12)

	clientNylonIP := "10.0.0.1"
	relayNylonIP := "10.0.0.2"
	exitNylonIP := "10.0.0.3"
	targetIP := "203.0.113.10"

	configDir := h.SetupTestDir()
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("client", clientKey.Pubkey(), clientNylonIP, clientIP),
			SimpleRouter("relay", relayKey.Pubkey(), relayNylonIP, relayIP),
			SimpleRouter("exit", exitKey.Pubkey(), exitNylonIP, exitIP),
		},
		Graph: []string{
			"client, relay",
			"relay, exit",
		},
		Timestamp:  time.Now().UnixNano(),
		ExcludeIPs: []netip.Prefix{netip.MustParsePrefix(h.Subnet)},
	}
	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	clientCfg := SimpleLocal("client", clientKey)
	clientCfg.ExitNode = "exit"
	clientPath := h.WriteConfig(configDir, "client.yaml", clientCfg)

	relayCfg := SimpleLocal("relay", relayKey)
	relayPath := h.WriteConfig(configDir, "relay.yaml", relayCfg)

	exitCfg := SimpleLocal("exit", exitKey)
	exitCfg.AdvertiseExitNode = true
	exitCfg.PreUp = append(exitCfg.PreUp, "ip addr add "+targetIP+"/32 dev lo")
	exitPath := h.WriteConfig(configDir, "exit.yaml", exitCfg)

	h.StartNodes(
		NodeSpec{Name: "client", IP: clientIP, CentralConfigPath: centralPath, NodeConfigPath: clientPath},
		NodeSpec{Name: "relay", IP: relayIP, CentralConfigPath: centralPath, NodeConfigPath: relayPath},
		NodeSpec{Name: "exit", IP: exitIP, CentralConfigPath: centralPath, NodeConfigPath: exitPath},
	)

	h.WaitForLog("client", "installing new route prefix=10.0.0.2/31")
	h.WaitForLog("relay", "installing new route prefix=10.0.0.1/32")
	h.WaitForLog("relay", "installing new route prefix=10.0.0.3/32")

	// the relay must not install a default route — it's neither
	// advertising an exit nor consuming one.
	relayRoutes, _, err := h.Exec("relay", []string{"ip", "route", "show", "dev", "nylon0"})
	if err != nil {
		t.Fatalf("failed to inspect relay route table: %v", err)
	}
	if strings.Contains(relayRoutes, "default") || strings.Contains(relayRoutes, "0.0.0.0/0") {
		t.Fatalf("relay unexpectedly installed default route:\n%s", relayRoutes)
	}

	h.StartTrace("client")
	h.StartTrace("relay")
	h.StartTrace("exit")

	ping := h.ExecBackground("client", []string{"ping", "-c", "3", targetIP})
	h.WaitForTrace("client", "ExitEncap: 10.0.0.1 -> 203.0.113.10, exit exit via relay")
	h.WaitForTrace("relay", "ExitTransit: origin client exit exit via exit")
	h.WaitForTrace("exit", "ExitDecap: origin client 10.0.0.1 -> 203.0.113.10")

	stdout, stderr, err := ping.Wait()
	if err != nil {
		h.PrintLogs("client")
		h.PrintLogs("relay")
		h.PrintLogs("exit")
		t.Fatalf("exit ping failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
	t.Logf("Ping output:\n%s", stdout)
}
