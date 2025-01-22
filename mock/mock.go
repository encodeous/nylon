package mock

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"github.com/encodeous/nylon/state"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

func GetMockWeight(a, b state.Node, cfg state.CentralCfg) []uint16 {
	var weights []uint16
	for _, edge := range cfg.MockWeights {
		if edge.V1 == a && edge.V2 == b || edge.V2 == a && edge.V2 == b {
			weights = append(weights, edge.V3)
		}
	}
	return weights
}

func MockCfg() (state.CentralCfg, []state.NodeCfg, error) {
	mockCentralCfg := state.CentralCfg{
		RootCa:  nil,
		Nodes:   make([]state.PubNodeCfg, 0),
		Version: 0,
	}
	basePort := 23000
	wgBasePort := 24000
	names := []string{
		"bob",
		"jeb",
		"kat",
		"eve",
		"ada",
	}
	nodes := make([]state.NodeCfg, 0)
	for i, node := range names {
		dpKey, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return state.CentralCfg{}, nil, err
		}
		ecKey, err := ecdh.X25519().NewPrivateKey(dpKey[:])
		if err != nil {
			return state.CentralCfg{}, nil, err
		}
		_, ctlKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return state.CentralCfg{}, nil, err
		}
		mockNode := state.NodeCfg{
			Id:      state.Node(node),
			CtlAddr: fmt.Sprintf("127.0.0.1:%d", basePort+i),
			DpAddr:  "127.0.0.1",
			Key:     state.EdPrivateKey(ctlKey),
			WgKey:   (*state.EcPrivateKey)(ecKey),
			WgPort:  wgBasePort + i,
		}
		nodes = append(nodes, mockNode)
		mockCentralCfg.Nodes = append(mockCentralCfg.Nodes, mockNode.GeneratePubCfg())
	}
	mockCentralCfg.Edges = []state.Pair[state.Node, state.Node]{
		{"bob", "jeb"},
		{"bob", "kat"},
		{"bob", "eve"},
		{"jeb", "kat"},
		{"kat", "ada"},
		{"kat", "eve"},
		{"eve", "ada"},
	}
	mockCentralCfg.MockWeights = []state.Triple[state.Node, state.Node, uint16]{
		{"bob", "jeb", 1},
		{"bob", "kat", 1},
		{"bob", "eve", 10},
		{"jeb", "kat", 1},
		{"kat", "ada", 1},
		{"kat", "eve", 1},
		{"eve", "ada", 2},
	}
	return mockCentralCfg, nodes, nil
}
