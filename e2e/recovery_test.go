//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
)

func TestRecoveryExample(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	t.Parallel()

	h := NewHarness(t)

	// Node names
	alice := "alice"
	bob := "bob"
	charlie := "charlie"
eve := "eve"
vps := "vps"
	nodeNames := []string{alice, bob, charlie, eve, vps}

	// Generate keys
	keys := make(map[string]state.NyPrivateKey)
	pubKeys := make(map[string]state.NyPublicKey)
	for _, name := range nodeNames {
		k := state.GenerateKey()
		keys[name] = k
		pubKeys[name] = k.Pubkey()
	}

	// Internal Nylon IPs (10.0.0.x)
	nylonIPs := make(map[string]string)
	for i, name := range nodeNames {
		nylonIPs[name] = fmt.Sprintf("10.0.0.%d", i+1)
	}

	// Docker IPs
	dockerIPs := make(map[string]string)
	for i, name := range nodeNames {
		dockerIPs[name] = GetIP(h.Subnet, 10+i)
	}

	configDir := h.SetupTestDir()

	// 1. Create Central Config
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter(alice, pubKeys[alice], nylonIPs[alice], dockerIPs[alice]),
			SimpleRouter(bob, pubKeys[bob], nylonIPs[bob], dockerIPs[bob]),
			SimpleRouter(charlie, pubKeys[charlie], nylonIPs[charlie], dockerIPs[charlie]),
			SimpleRouter(eve, pubKeys[eve], nylonIPs[eve], dockerIPs[eve]),
			SimpleRouter(vps, pubKeys[vps], nylonIPs[vps], dockerIPs[vps]),
		},
		Graph: []string{
			"vps, charlie",
			"vps, alice",
			"eve, bob",
			"vps, eve",
			"alice, bob",
		},
		Timestamp: time.Now().UnixNano(),
	}

	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	// 2. Create Node Configs & Start Nodes
	nodeSpecs := make([]NodeSpec, 0, len(nodeNames))
	for _, name := range nodeNames {
		cfg := SimpleLocal(name, keys[name])
		cfgPath := h.WriteConfig(configDir, name+".yaml", cfg)
		nodeSpecs = append(nodeSpecs, NodeSpec{
			Name:              name,
			IP:                dockerIPs[name],
			CentralConfigPath: centralPath,
			NodeConfigPath:    cfgPath,
		})
	}

	h.StartNodes(nodeSpecs...)

	// 3. Wait for full convergence
	t.Log("Waiting for initial convergence...")
	h.WaitForLog(alice, fmt.Sprintf("new.prefix=%s/32", nylonIPs[bob]))

	// 4. Verify connectivity Alice -> Bob
	t.Log("Verifying initial connectivity Alice -> Bob (Direct)")
	stdout, stderr, err := h.Exec(alice, []string{"ping", "-c", "3", nylonIPs[bob]})
	if err != nil {
		t.Fatalf("Initial ping failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}

	// Check that traffic went directly to Bob
	h.WaitForLog(alice, fmt.Sprintf("Fwd packet: %s -> %s, via %s", nylonIPs[alice], nylonIPs[bob], bob))

	// 5. Break the link Alice-Bob
	t.Log("Breaking link Alice <-> Bob")
	_, _, err = h.Exec(alice, []string{"ip", "route", "add", "blackhole", dockerIPs[bob] + "/32"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = h.Exec(bob, []string{"ip", "route", "add", "blackhole", dockerIPs[alice] + "/32"})
	if err != nil {
		t.Fatal(err)
	}

	// 6. Wait for recovery
	t.Log("Waiting for recovery (rerouting)...")
	deadline := time.Now().Add(1 * time.Minute)
	recovered := false
	for time.Now().Before(deadline) {
		h.Exec(alice, []string{"ping", "-c", "1", "-W", "1", nylonIPs[bob]})

		h.mu.Lock()
		buf := h.LogBuffers[alice].String()
		h.mu.Unlock()

		if strings.Contains(buf, fmt.Sprintf("Fwd packet: %s -> %s, via %s", nylonIPs[alice], nylonIPs[bob], vps)) {
			recovered = true
			break
		}
		time.Sleep(1 * time.Second)
	}

	if !recovered {
		h.PrintLogs(alice)
		t.Fatal("Failed to recover route via VPS")
	}

	t.Log("Recovery successful! Traffic rerouted via VPS.")

	// Final connectivity check
	stdout, stderr, err = h.Exec(alice, []string{"ping", "-c", "3", nylonIPs[bob]})
	if err != nil {
		t.Fatalf("Post-recovery ping failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
}