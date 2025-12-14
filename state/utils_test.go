package state

import (
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func SampleNetwork(t *testing.T, numClients, numRouters int, fullyConnected bool) (CentralCfg, map[string]NyPrivateKey) {
	t.Helper()
	keyStore := make(map[string]NyPrivateKey)
	keyStore["dist"] = GenerateKey()
	cfg := CentralCfg{
		Dist: &DistributionCfg{
			Key: keyStore["dist"].Pubkey(),
			Repos: []string{
				"https://example.com",
				"file:example.conf",
			},
		},
	}

	clients := make([]string, numClients)

	for idx := range numClients {
		client := fmt.Sprintf("client-%d", idx)
		clients[idx] = client
		keyStore[client] = GenerateKey()
		cfg.Clients = append(cfg.Clients, ClientCfg{NodeCfg{
			Id:       NodeId(client),
			PubKey:   keyStore[client].Pubkey(),
			Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.1.0.%d/32", idx))},
		}})
	}

	routers := make([]string, numRouters)

	for idx := range numRouters {
		router := fmt.Sprintf("router-%d", idx)
		routers[idx] = router
		keyStore[router] = GenerateKey()
		cfg.Routers = append(cfg.Routers, RouterCfg{
			NodeCfg: NodeCfg{
				Id:       NodeId(router),
				PubKey:   keyStore[router].Pubkey(),
				Prefixes: []netip.Prefix{netip.MustParsePrefix(fmt.Sprintf("10.1.0.%d/32", idx))},
			},
			Endpoints: []netip.AddrPort{
				netip.MustParseAddrPort(fmt.Sprintf("192.168.0.%d:25565", idx)),
			},
		})
	}

	cfg.Timestamp = time.Now().UnixNano()
	cfg.Graph = append(cfg.Graph, fmt.Sprintf("clients = %s", strings.Join(clients, ",")))
	cfg.Graph = append(cfg.Graph, fmt.Sprintf("routers = %s", strings.Join(routers, ",")))
	if fullyConnected {
		cfg.Graph = append(cfg.Graph, "all, all")
		cfg.Graph = append(cfg.Graph, "all = clients, routers")
	} else {
		cfg.Graph = append(cfg.Graph, "clients, routers")
	}

	return cfg, keyStore
}

func SampleEnv(cfg *CentralCfg, keyStore map[string]NyPrivateKey, node NodeId) *Env {
	return &Env{
		DispatchChannel: nil,
		CentralCfg:      *cfg,
		LocalCfg: LocalCfg{
			Key:            keyStore[string(node)],
			Id:             node,
			Port:           5000,
			NoNetConfigure: false,
		},
		Context:  nil,
		Cancel:   nil,
		Log:      nil,
		Updating: atomic.Bool{},
	}
}
