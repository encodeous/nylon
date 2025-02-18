package state

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"net/netip"
	"time"
)

func GetMockWeight(a, b Node, cfg CentralCfg) []*time.Duration {
	var weights []*time.Duration
	for _, edge := range cfg.mockWeights {
		if edge.V1 == a && edge.V2 == b || edge.V2 == a && edge.V2 == b {
			weights = append(weights, edge.V3)
		}
	}
	return weights
}

func GetMinMockWeight(a, b Node, cfg CentralCfg) time.Duration {
	weight := time.Second * 0
	for _, edge := range cfg.mockWeights {
		if edge.V1 == a && edge.V2 == b || edge.V2 == a && edge.V1 == b {
			weight = max(weight, *edge.V3)
		}
	}
	return weight
}

func Box(v int) *time.Duration {
	pt := new(time.Duration)
	*pt = time.Millisecond * time.Duration(v)
	return pt
}

func MockCfg() (CentralCfg, []NodeCfg, error) {
	mockCentralCfg := CentralCfg{
		RootPubKey: nil,
		Nodes:      make([]PubNodeCfg, 0),
		Version:    0,
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
	nodes := make([]NodeCfg, 0)
	for i, node := range names {
		dpKey, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			return CentralCfg{}, nil, err
		}
		ecKey, err := ecdh.X25519().NewPrivateKey(dpKey[:])
		if err != nil {
			return CentralCfg{}, nil, err
		}
		_, ctlKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return CentralCfg{}, nil, err
		}
		dpAddr, err := netip.ParseAddrPort(fmt.Sprintf("127.0.0.1:%d", wgBasePort+i))
		ctlBind, err := netip.ParseAddrPort(fmt.Sprintf("127.0.0.1:%d", basePort+i))
		if err != nil {
			return CentralCfg{}, nil, err
		}
		mockNode := NodeCfg{
			Id:      Node(node),
			CtlBind: ctlBind,
			DpPort:  dpAddr.Port(),
			Key:     EdPrivateKey(ctlKey),
			WgKey:   (*EcPrivateKey)(ecKey),
		}
		nodes = append(nodes, mockNode)
		mockCentralCfg.Nodes = append(mockCentralCfg.Nodes, mockNode.GeneratePubCfg(netip.MustParseAddr("127.0.0.1"), netip.MustParseAddr("10.99.34."+string(rune(i+'0')))))
	}
	mockCentralCfg.Edges = []Pair[Node, Node]{
		{"bob", "jeb"},
		{"bob", "kat"},
		{"bob", "eve"},
		{"jeb", "kat"},
		{"kat", "ada"},
		{"kat", "eve"},
		{"eve", "ada"},
	}
	mockCentralCfg.mockWeights = []Triple[Node, Node, *time.Duration]{
		{"bob", "jeb", Box(7)},
		{"bob", "kat", Box(9)},
		{"bob", "eve", Box(100)},
		{"jeb", "kat", Box(1)},
		{"kat", "ada", Box(10)},
		{"kat", "eve", Box(3)},
		{"eve", "ada", Box(8)},
	}
	return mockCentralCfg, nodes, nil
}
