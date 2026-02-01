//go:build e2e

package e2e

import (
	"context"
	"net/netip"
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
	h := NewHarness(t)
	// Cleanup is handled by Harness via t.Cleanup

	ctx := context.Background()

	// 1. Setup Keys
	privKey := state.GenerateKey()
	pubKey := privKey.Pubkey()

	// 2. Prepare Directories
	runDir := filepath.Join(h.RootDir, "e2e", "runs", t.Name())
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}

	// 3. Prepare Initial Bundle (v1)
	distCfg := &state.DistributionCfg{
		Key:   pubKey,
		Repos: []string{"http://repo:80/bundle"},
	}
	
	nodeKey := state.GenerateKey()
	nodeId := "node-1"

	cfg1 := state.CentralCfg{
		Timestamp: 1,
		Dist:      distCfg,
		Routers: []state.RouterCfg{
			{
				NodeCfg: state.NodeCfg{
					Id:     state.NodeId(nodeId),
					PubKey: nodeKey.Pubkey(),
				},
				Endpoints: []netip.AddrPort{},
			},
		},
		Clients: []state.ClientCfg{},
		Graph:   []string{},
	}

	cfg1Bytes, err := yaml.Marshal(cfg1)
	if err != nil {
		t.Fatal(err)
	}
	bundle1Str, err := state.BundleConfig(string(cfg1Bytes), privKey)
	if err != nil {
		t.Fatal(err)
	}
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
	// Write central.yaml (v1) to disk for initial startup
	centralConfigPath := filepath.Join(runDir, "central.yaml")
	if err := os.WriteFile(centralConfigPath, cfg1Bytes, 0644); err != nil {
		t.Fatal(err)
	}

	// Write node.yaml
	nodeCfg := state.LocalCfg{
		Id:   state.NodeId(nodeId),
		Key:  nodeKey,
		Port: 51820,
		// We can omit Dist here since we are providing central.yaml,
		// but providing it doesn't hurt.
		Dist: &state.LocalDistributionCfg{
			Key: pubKey,
			Url: "http://repo:80/bundle",
		},
	}
	nodeCfgBytes, err := yaml.Marshal(nodeCfg)
	if err != nil {
		t.Fatal(err)
	}
	nodeConfigPath := filepath.Join(runDir, "node.yaml")
	if err := os.WriteFile(nodeConfigPath, nodeCfgBytes, 0644); err != nil {
		t.Fatal(err)
	}

	t.Log("Starting Nylon Node...")
	h.StartNode(nodeId, "", centralConfigPath, nodeConfigPath)

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
	
	t.Log("Waiting for restart...")
	h.WaitForLog(nodeId, "Restarting Nylon...")

	// Allow some time for the restart to complete and write the file
	time.Sleep(5 * time.Second)

	t.Log("Verifying config version on node...")
	stdout, _, err := h.Exec(nodeId, []string{"cat", "/app/config/central.yaml"})
	if err != nil {
		t.Fatal(err)
	}

	var verifyCfg state.CentralCfg
	if err := yaml.Unmarshal([]byte(stdout), &verifyCfg); err != nil {
		t.Fatalf("Failed to parse config from node: %v", err)
	}

	if verifyCfg.Timestamp != bundle2Timestamp {
		t.Fatalf("Expected timestamp %d, got %d. Config content: %s", bundle2Timestamp, verifyCfg.Timestamp, stdout)
	}
	t.Logf("Successfully updated to timestamp %d.", verifyCfg.Timestamp)
}
