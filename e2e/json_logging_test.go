//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
)

func TestJSONLogging(t *testing.T) {
	h := NewHarness(t)

	node1Key := state.GenerateKey()
	node2Key := state.GenerateKey()

	node1IP := GetIP(h.Subnet, 10)
	node2IP := GetIP(h.Subnet, 11)

	node1NylonIP := "10.0.0.1"
	node2NylonIP := "10.0.0.2"

	configDir := h.SetupTestDir()

	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("node1", node1Key.Pubkey(), node1NylonIP, node1IP),
			SimpleRouter("node2", node2Key.Pubkey(), node2NylonIP, node2IP),
		},
		Graph: []string{
			"node1, node2",
		},
		Timestamp: time.Now().UnixNano(),
	}

	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	node1Cfg := SimpleLocal("node1", node1Key)
	node1Path := h.WriteConfig(configDir, "node1.yaml", node1Cfg)

	node2Cfg := SimpleLocal("node2", node2Key)
	node2Path := h.WriteConfig(configDir, "node2.yaml", node2Cfg)

	h.StartNodes(
		NodeSpec{
			Name:              "node1",
			IP:                node1IP,
			CentralConfigPath: centralPath,
			NodeConfigPath:    node1Path,
			ExtraArgs:         []string{"--json"},
		},
		NodeSpec{
			Name:              "node2",
			IP:                node2IP,
			CentralConfigPath: centralPath,
			NodeConfigPath:    node2Path,
			ExtraArgs:         []string{"--json"},
		},
	)

	t.Log("Waiting for JSON log pattern...")
	h.WaitForMatch("node1", `\{"time":".*","level":".*","msg":".*"`)
	h.WaitForMatch("node2", `\{"time":".*","level":".*","msg":".*"`)
}
