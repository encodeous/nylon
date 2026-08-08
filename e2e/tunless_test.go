//go:build e2e

package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
)

func TestTUNLessRelay(t *testing.T) {
	t.Parallel()
	h := NewHarness(t)

	sourceKey := state.GenerateKey()
	relayKey := state.GenerateKey()
	destinationKey := state.GenerateKey()

	sourceIP := GetIP(h.Subnet, 10)
	relayIP := GetIP(h.Subnet, 11)
	destinationIP := GetIP(h.Subnet, 12)

	const (
		sourceNylonIP      = "10.0.0.1"
		destinationNylonIP = "10.0.0.3"
	)

	configDir := h.SetupTestDir()
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("source", sourceKey.Pubkey(), sourceNylonIP, ""),
			{
				NodeCfg: state.NodeCfg{
					Id:     "relay",
					PubKey: relayKey.Pubkey(),
				},
				Endpoints: []string{fmt.Sprintf("%s:57175", relayIP)},
			},
			SimpleRouter("destination", destinationKey.Pubkey(), destinationNylonIP, ""),
		},
		Graph: []string{
			"source, relay",
			"relay, destination",
		},
		Timestamp: time.Now().UnixNano(),
	}
	centralPath := h.WriteConfig(configDir, "central.yaml", central)

	sourceCfg := SimpleLocal("source", sourceKey)
	relayCfg := SimpleLocal("relay", relayKey)
	relayCfg.NoTun = true
	destinationCfg := SimpleLocal("destination", destinationKey)

	h.StartNodes(
		NodeSpec{Name: "source", IP: sourceIP, CentralConfigPath: centralPath, NodeConfigPath: h.WriteConfig(configDir, "source.yaml", sourceCfg)},
		NodeSpec{Name: "relay", IP: relayIP, CentralConfigPath: centralPath, NodeConfigPath: h.WriteConfig(configDir, "relay.yaml", relayCfg)},
		NodeSpec{Name: "destination", IP: destinationIP, CentralConfigPath: centralPath, NodeConfigPath: h.WriteConfig(configDir, "destination.yaml", destinationCfg)},
	)

	h.WaitForStatus(t, "source", func(status *protocol.StatusResponse) bool {
		return HasSelectedRoute(status, destinationNylonIP+"/32", "relay", "destination")
	})
	h.WaitForStatus(t, "destination", func(status *protocol.StatusResponse) bool {
		return HasSelectedRoute(status, sourceNylonIP+"/32", "relay", "source")
	})

	h.StartTrace("relay")
	ttlPing := h.ExecBackground("source", []string{"ping", "-c", "10", "-W", "1", "-t", "1", destinationNylonIP})
	h.WaitForTrace("relay", fmt.Sprintf("TTL Expired: %s -> %s", sourceNylonIP, destinationNylonIP))
	_, _, _ = ttlPing.Wait()

	stdout, stderr, err := h.Exec("source", []string{"ping", "-c", "3", "-W", "2", destinationNylonIP})
	if err != nil {
		t.Fatalf("ping through TUN-less relay failed: %v\nStdout: %s\nStderr: %s", err, stdout, stderr)
	}
}
