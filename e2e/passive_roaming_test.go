//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPassiveRoaming(t *testing.T) {
	h := NewHarness(t)
	ctx := context.Background()

	ip1 := GetIP(h.Subnet, 2)
	ip2 := GetIP(h.Subnet, 3)
	ip3 := GetIP(h.Subnet, 4)
	clientContainerIP := GetIP(h.Subnet, 5)

	keys := make(map[string]state.NyPrivateKey)
	pubKeys := make(map[string]state.NyPublicKey)
	nodes := []string{"node-1", "node-2", "node-3", "client-4"}

	for _, n := range nodes {
		k := state.GenerateKey()
		keys[n] = k
		pubKeys[n] = k.Pubkey()
	}

	centralCfg := state.CentralCfg{
		Routers: []state.RouterCfg{
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-1",
					PubKey:    pubKeys["node-1"],
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1")},
				},
				Endpoints: []*state.DynamicEndpoint{state.NewDynamicEndpoint(fmt.Sprintf("%s:51820", ip1))},
			},
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-2",
					PubKey:    pubKeys["node-2"],
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.2")},
				},
				Endpoints: []*state.DynamicEndpoint{state.NewDynamicEndpoint(fmt.Sprintf("%s:51820", ip2))},
			},
			{
				NodeCfg: state.NodeCfg{
					Id:        "node-3",
					PubKey:    pubKeys["node-3"],
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.3")},
				},
				Endpoints: []*state.DynamicEndpoint{state.NewDynamicEndpoint(fmt.Sprintf("%s:51820", ip3))},
			},
		},
		Clients: []state.ClientCfg{
			{
				NodeCfg: state.NodeCfg{
					Id:        "client-4",
					PubKey:    pubKeys["client-4"],
					Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.4")},
				},
			},
		},
		Graph: []string{
			"node-1, node-2",
			"node-2, node-3",
			"node-2, client-4",
			"node-3, client-4",
		},
		Timestamp: time.Now().UnixNano(),
	}

	centralBytes, err := yaml.Marshal(centralCfg)
	if err != nil {
		t.Fatal(err)
	}

	var nodeSpecs []NodeSpec
	for _, n := range []string{"node-1", "node-2", "node-3"} {
		nodeCfg := state.LocalCfg{
			Key:            keys[n],
			Id:             state.NodeId(n),
			Port:           51820,
			InterfaceName:  "nylon",
			NoNetConfigure: false,
		}
		nodeBytes, err := yaml.Marshal(nodeCfg)
		if err != nil {
			t.Fatal(err)
		}

		centralFile := CreateTempFile(t, h.RootDir, "central-"+n+".yaml", centralBytes)
		nodeFile := CreateTempFile(t, h.RootDir, "node-"+n+".yaml", nodeBytes)

		var containerIP string
		switch n {
		case "node-1":
			containerIP = ip1
		case "node-2":
			containerIP = ip2
		case "node-3":
			containerIP = ip3
		}

		nodeSpecs = append(nodeSpecs, NodeSpec{
			Name:              n,
			IP:                containerIP,
			CentralConfigPath: centralFile,
			NodeConfigPath:    nodeFile,
		})
	}

	h.StartNodes(nodeSpecs...)

	t.Log("Nylon nodes started. Waiting for convergence...")
	time.Sleep(5 * time.Second)

	t.Logf("Starting Client Node at %s", clientContainerIP)

	req := testcontainers.ContainerRequest{
		Image:    ImageName,
		Networks: []string{h.Network.Name},
		NetworkAliases: map[string][]string{
			h.Network.Name: {"client-4"},
		},
		Entrypoint: []string{"/bin/sh", "-c", "sleep infinity"},
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.Privileged = true
			hostConfig.CapAdd = []string{"NET_ADMIN"}
		},
		EndpointSettingsModifier: func(m map[string]*network.EndpointSettings) {
			if s, ok := m[h.Network.Name]; ok {
				s.IPAMConfig = &network.EndpointIPAMConfig{
					IPv4Address: clientContainerIP,
				}
			}
		},
	}

	clientContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start client container: %v", err)
	}
	h.Nodes["client-4"] = clientContainer
	t.Cleanup(func() {
		clientContainer.Terminate(ctx)
	})

	execClient := func(cmd string) {
		t.Logf("[client-4] Exec: %s", cmd)
		_, _, err := h.Exec("client-4", []string{"/bin/sh", "-c", cmd})
		if err != nil {
			t.Fatalf("Client exec failed: %v", err)
		}
	}

	pingNode1 := func() error {
		code, _, err := clientContainer.Exec(ctx, []string{"ping", "-c", "1", "-W", "1", "10.0.0.1"})
		if err != nil {
			return err
		}
		if code != 0 {
			return fmt.Errorf("ping failed with code %d", code)
		}
		return nil
	}

	waitForPing := func() {
		t.Log("Waiting for connectivity to Node 1...")
		timeout := time.After(30 * time.Second)
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				t.Fatal("Timeout waiting for connectivity")
			case <-ticker.C:
				if err := pingNode1(); err == nil {
					t.Log("Connectivity established!")
					return
				}
			}
		}
	}

	t.Log("=== Phase 1: Connect Client to Node 3 ===")

	privKeyStr, _ := keys["client-4"].MarshalText()
	node3PubStr, _ := pubKeys["node-3"].MarshalText()
	node2PubStr, _ := pubKeys["node-2"].MarshalText()

	execClient("ip link add dev wg0 type wireguard")
	execClient("ip address add dev wg0 10.0.0.4/32")
	execClient(fmt.Sprintf("echo %s > /tmp/wg_priv && wg set wg0 private-key /tmp/wg_priv && rm /tmp/wg_priv", string(privKeyStr)))
	execClient("ip link set up dev wg0")

	execClient(fmt.Sprintf("wg set wg0 peer %s endpoint %s:51820 allowed-ips 0.0.0.0/0 persistent-keepalive 5", string(node3PubStr), ip3))

	execClient("ip route add 10.0.0.0/24 dev wg0")

	waitForPing()

	t.Log("=== Phase 2: Roam Client to Node 2 ===")

	execClient(fmt.Sprintf("wg set wg0 peer %s remove", string(node3PubStr)))

	execClient(fmt.Sprintf("wg set wg0 peer %s endpoint %s:51820 allowed-ips 0.0.0.0/0 persistent-keepalive 5", string(node2PubStr), ip2))

	t.Log("Sending traffic to trigger roaming update...")

	waitForPing()

	t.Log("=== Test Complete: Passive Roaming Successful ===")
}

func CreateTempFile(t *testing.T, dir, name string, content []byte) string {
	runDir := filepath.Join(dir, "e2e", "runs", t.Name())
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	fPath := filepath.Join(runDir, name)
	if err := os.WriteFile(fPath, content, 0644); err != nil {
		t.Fatal(err)
	}
	return fPath
}

var _ = wait.ForLog
