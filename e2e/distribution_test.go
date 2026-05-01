//go:build e2e

package e2e

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestDistribution(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)
	ctx := context.Background()

	// 1. Setup Keys
	privKey := state.GenerateKey()
	pubKey := privKey.Pubkey()

	// 2. Prepare Directories
	runDir := h.SetupTestDir()

	// 3. Prepare Initial Bundle (v1)
	distCfg := &state.DistributionCfg{
		Key:   pubKey,
		Repos: []string{"http://repo:80/bundle"},
	}

	nodeKey := state.GenerateKey()
	nodeId := "node-1"
	nodeIP := GetIP(h.Subnet, 10)

	cfg1 := state.CentralCfg{
		Timestamp: 1,
		Dist:      distCfg,
		Routers: []state.RouterCfg{
			SimpleRouter(nodeId, nodeKey.Pubkey(), "10.0.0.1", ""),
		},
	}

	cfg1Bytes, err := yaml.Marshal(cfg1)
	if err != nil {
		t.Fatal(err)
	}
	bundle1Str, err := state.BundleConfig(string(cfg1Bytes), privKey)
	if err != nil {
		t.Fatal(err)
	}

	// Ensure cfg1 has the same timestamp as bundle1 to prevent immediate update
	unbundled1, err := state.UnbundleConfig(bundle1Str, pubKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg1.Timestamp = unbundled1.Timestamp

	bundle1Path := filepath.Join(runDir, "bundle1")
	if err := os.WriteFile(bundle1Path, []byte(bundle1Str), 0644); err != nil {
		t.Fatal(err)
	}

	// 4. Start Repo Server
	t.Log("Starting Repo Server...")
	repoReq := testcontainers.ContainerRequest{
		Image:        "python:3-alpine",
		Cmd:          []string{"sh", "-c", "mkdir -p /data && cd /data && python3 -u -m http.server 80"},
		ExposedPorts: []string{"80/tcp"},
		Networks:     []string{h.Network.Name},
		NetworkAliases: map[string][]string{
			h.Network.Name: {"repo"},
		},
		WaitingFor: wait.ForListeningPort("80/tcp"),
	}
	repoContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: repoReq,
		Started:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		repoContainer.Terminate(context.Background())
	})

	// Copy bundle1 to repo
	if err := repoContainer.CopyFileToContainer(ctx, bundle1Path, "/data/bundle", 0644); err != nil {
		t.Fatal(err)
	}

	// 5. Start Nylon Node
	centralConfigPath := h.WriteConfig(runDir, "central.yaml", cfg1)

	// Write node.yaml
	nodeCfg := SimpleLocal(nodeId, nodeKey)
	nodeCfg.Dist = &state.LocalDistributionCfg{
		Key: pubKey,
		Url: "http://repo:80/bundle",
	}
	nodeConfigPath := h.WriteConfig(runDir, "node.yaml", nodeCfg)

	t.Log("Starting Nylon Node...")
	h.StartNodes(NodeSpec{
		Name:              nodeId,
		IP:                nodeIP,
		CentralConfigPath: centralConfigPath,
		NodeConfigPath:    nodeConfigPath,
	})

	// Wait for start
	h.WaitForLog(nodeId, "Nylon has been initialized")
	t.Log("Nylon Node started (v1).")

	// 6. Create and Push Bundle 2
	t.Log("Preparing Bundle 2...")
	// Wait a bit to ensure timestamp is different if using UnixNano
	time.Sleep(1 * time.Second)

	cfg2 := cfg1
	// BundleConfig will overwrite this timestamp anyway
	cfg2Bytes, err := yaml.Marshal(cfg2)
	if err != nil {
		t.Fatal(err)
	}
	bundle2Str, err := state.BundleConfig(string(cfg2Bytes), privKey)
	if err != nil {
		t.Fatal(err)
	}

	// We need to know the timestamp of bundle 2 to verify
	unbundled2, err := state.UnbundleConfig(bundle2Str, pubKey)
	if err != nil {
		t.Fatal(err)
	}
	bundle2Timestamp := unbundled2.Timestamp

	bundle2Path := filepath.Join(runDir, "bundle2")
	if err := os.WriteFile(bundle2Path, []byte(bundle2Str), 0644); err != nil {
		t.Fatal(err)
	}

	t.Logf("Updating Repo with Bundle 2 (timestamp: %d)...", bundle2Timestamp)
	if err := repoContainer.CopyFileToContainer(ctx, bundle2Path, "/data/bundle", 0644); err != nil {
		t.Fatal(err)
	}

	// 7. Verify Update
	t.Log("Waiting for update detection...")
	h.WaitForLog(nodeId, "Found a new config update in repo")

	var verifyCfg state.CentralCfg
	var stdout string
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		stdout, _, err = h.Exec(nodeId, []string{"cat", "/app/config/central.yaml"})
		if err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal([]byte(stdout), &verifyCfg); err != nil {
			t.Fatalf("Failed to parse config from node: %v", err)
		}
		if verifyCfg.Timestamp == bundle2Timestamp {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}

	if verifyCfg.Timestamp != bundle2Timestamp {
		t.Fatalf("Expected timestamp %d, got %d. Config content: %s", bundle2Timestamp, verifyCfg.Timestamp, stdout)
	}
	t.Logf("Successfully updated to timestamp %d.", verifyCfg.Timestamp)
}

func TestDistributionRejectsLocalNodeRemoval(t *testing.T) {
	t.Parallel()
	h, repoContainer, runDir, privKey, pubKey, nodeId, originalTimestamp := startDistributedSingleNode(t)
	ctx := context.Background()

	cfg2 := state.CentralCfg{
		Timestamp: originalTimestamp + 1,
		Dist: &state.DistributionCfg{
			Key:   pubKey,
			Repos: []string{"http://repo:80/bundle"},
		},
		Routers: nil,
	}
	bundlePath, _ := writeBundle(t, runDir, "bundle-local-node-removed", cfg2, privKey)
	if err := repoContainer.CopyFileToContainer(ctx, bundlePath, "/data/bundle", 0644); err != nil {
		t.Fatal(err)
	}

	h.WaitForLog(string(nodeId), "Found a new config update in repo")
	h.WaitForLog(string(nodeId), "failed to apply central config update")
	assertCentralTimestampStays(t, h, nodeId, originalTimestamp)
}

func TestDistributionRejectsInvalidCentralConfig(t *testing.T) {
	t.Parallel()
	h, repoContainer, runDir, privKey, pubKey, nodeId, originalTimestamp := startDistributedSingleNode(t)
	ctx := context.Background()

	invalid := state.CentralCfg{
		Timestamp: originalTimestamp + 1,
		Dist: &state.DistributionCfg{
			Key:   pubKey,
			Repos: []string{"http://repo:80/bundle"},
		},
		Routers: []state.RouterCfg{
			SimpleRouter(string(nodeId), state.GenerateKey().Pubkey(), "10.0.0.1", ""),
		},
		Graph: []string{"does-not-exist, " + string(nodeId)},
	}
	bundlePath := writeRawBundle(t, runDir, "bundle-invalid", invalid, privKey)
	if err := repoContainer.CopyFileToContainer(ctx, bundlePath, "/data/bundle", 0644); err != nil {
		t.Fatal(err)
	}

	h.WaitForLog(string(nodeId), "Error updating config")
	assertCentralTimestampStays(t, h, nodeId, originalTimestamp)
}

func startDistributedSingleNode(t *testing.T) (*Harness, testcontainers.Container, string, state.NyPrivateKey, state.NyPublicKey, state.NodeId, int64) {
	t.Helper()
	h := NewHarness(t)
	ctx := context.Background()
	privKey := state.GenerateKey()
	pubKey := privKey.Pubkey()
	runDir := h.SetupTestDir()

	nodeKey := state.GenerateKey()
	nodeId := state.NodeId("node-1")
	cfg := state.CentralCfg{
		Timestamp: 1,
		Dist: &state.DistributionCfg{
			Key:   pubKey,
			Repos: []string{"http://repo:80/bundle"},
		},
		Routers: []state.RouterCfg{
			SimpleRouter(string(nodeId), nodeKey.Pubkey(), "10.0.0.1", ""),
		},
	}
	bundlePath, timestamp := writeBundle(t, runDir, "bundle-initial", cfg, privKey)

	repoContainer := startBundleRepo(t, h, ctx, bundlePath)
	centralConfigPath := h.WriteConfig(runDir, "central.yaml", withTimestamp(cfg, timestamp))
	nodeCfg := SimpleLocal(string(nodeId), nodeKey)
	nodeCfg.Dist = &state.LocalDistributionCfg{
		Key: pubKey,
		Url: "http://repo:80/bundle",
	}
	nodeConfigPath := h.WriteConfig(runDir, "node.yaml", nodeCfg)

	h.StartNodes(NodeSpec{
		Name:              string(nodeId),
		IP:                GetIP(h.Subnet, 10),
		CentralConfigPath: centralConfigPath,
		NodeConfigPath:    nodeConfigPath,
	})
	h.WaitForLog(string(nodeId), "Nylon has been initialized")

	return h, repoContainer, runDir, privKey, pubKey, nodeId, timestamp
}

func startBundleRepo(t *testing.T, h *Harness, ctx context.Context, bundlePath string) testcontainers.Container {
	t.Helper()
	repoReq := testcontainers.ContainerRequest{
		Image:        "python:3-alpine",
		Cmd:          []string{"sh", "-c", "mkdir -p /data && cd /data && python3 -u -m http.server 80"},
		ExposedPorts: []string{"80/tcp"},
		Networks:     []string{h.Network.Name},
		NetworkAliases: map[string][]string{
			h.Network.Name: {"repo"},
		},
		WaitingFor: wait.ForListeningPort("80/tcp"),
	}
	repoContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: repoReq,
		Started:          true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		repoContainer.Terminate(context.Background())
	})
	if err := repoContainer.CopyFileToContainer(ctx, bundlePath, "/data/bundle", 0644); err != nil {
		t.Fatal(err)
	}
	return repoContainer
}

func writeBundle(t *testing.T, runDir string, name string, cfg state.CentralCfg, key state.NyPrivateKey) (string, int64) {
	t.Helper()
	cfgBytes, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bundleStr, err := state.BundleConfig(string(cfgBytes), key)
	if err != nil {
		t.Fatal(err)
	}
	unbundled, err := state.UnbundleConfig(bundleStr, key.Pubkey())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runDir, name)
	if err := os.WriteFile(path, []byte(bundleStr), 0644); err != nil {
		t.Fatal(err)
	}
	return path, unbundled.Timestamp
}

func writeRawBundle(t *testing.T, runDir string, name string, cfg state.CentralCfg, key state.NyPrivateKey) string {
	t.Helper()
	cfgBytes, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := state.SignBundle(cfgBytes, key)
	if err != nil {
		t.Fatal(err)
	}
	pub := key.Pubkey()
	bundle, err = state.SealBundle(bundle, pub[:])
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runDir, name)
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(bundle)), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func withTimestamp(cfg state.CentralCfg, timestamp int64) state.CentralCfg {
	cfg.Timestamp = timestamp
	return cfg
}

func assertCentralTimestampStays(t *testing.T, h *Harness, nodeId state.NodeId, expected int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := readCentralTimestamp(t, h, nodeId)
		if got != expected {
			t.Fatalf("expected central config timestamp to remain %d, got %d", expected, got)
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func readCentralTimestamp(t *testing.T, h *Harness, nodeId state.NodeId) int64 {
	t.Helper()
	stdout, _, err := h.Exec(string(nodeId), []string{"cat", "/app/config/central.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	var cfg state.CentralCfg
	if err := yaml.Unmarshal([]byte(stdout), &cfg); err != nil {
		t.Fatalf("Failed to parse config from node: %v", err)
	}
	return cfg.Timestamp
}
