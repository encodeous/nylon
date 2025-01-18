//go:generate protoc -I . --go_out=. ./core/protocol/nylon.proto
package main

import (
	"fmt"
	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/state"
	"log/slog"
	"sync"
)

//func mock() (*state.CentralCfg, *state.NodeCfg, error) {
//	_, nodeKey, err := ed25519.GenerateKey(nil)
//	if err != nil {
//		return nil, nil, err
//	}
//
//	certTemplate := x509.Certificate{
//		PublicKey: nodeKey.Public(),
//		Subject: pkix.Name{
//			CommonName: "dummyNode",
//		},
//		IsCA:         false,
//		SubjectKeyId: nil,
//		NotBefore:    time.Now(),
//		NotAfter:     time.Now().AddDate(10, 0, 0),
//		SerialNumber: big.NewInt(time.Now().Unix()),
//	}
//
//	ss, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, certTemplate.PublicKey, nodeKey)
//	if err != nil {
//		return nil, nil, err
//	}
//
//	dpKey, err := ecdh.X25519().GenerateKey(rand.Reader)
//	mockNode := state.NodeCfg{
//		Id:    "currentNode",
//		Key:   state.EdPrivateKey(nodeKey),
//		DpKey: (*state.EcPrivateKey)(dpKey),
//		Cert:  state.Cert(ss),
//	}
//
//	mockPubNode := mockNode.GetPubNodeCfg()
//
//	mockCentralCfg := state.CentralCfg{
//		RootCa: ss,
//		Nodes: []state.PubNodeCfg{
//			mockPubNode,
//		},
//		Version: 0,
//	}
//
//	return &mockCentralCfg, &mockNode, nil
//}

func mock() (state.CentralCfg, []state.NodeCfg, error) {
	mockCentralCfg := state.CentralCfg{
		RootCa:  nil,
		Nodes:   make([]state.PubNodeCfg, 0),
		Version: 0,
	}
	basePort := 23000
	names := []string{
		"bob",
		"jeb",
		"kat",
		"eve",
		"eli",
		"ada",
	}
	nodes := make([]state.NodeCfg, 0)
	for i, node := range names {
		mockNode := state.NodeCfg{
			Id:      state.Node(node),
			CtlAddr: fmt.Sprintf("127.0.0.1:%d", basePort+i),
		}
		nodes = append(nodes, mockNode)
		mockCentralCfg.Nodes = append(mockCentralCfg.Nodes, mockNode.GetPubNodeCfg())
	}
	mockCentralCfg.Edges = []state.Pair[state.Node, state.Node]{
		{"bob", "jeb"},
		{"bob", "kat"},
		{"jeb", "eve"},
		{"jeb", "kat"},
		{"jeb", "eli"},
		{"jeb", "ada"},
	}
	return mockCentralCfg, nodes, nil
}

func main() {
	ccfg, ncfg, err := mock()
	if err != nil {
		panic(err)
	}
	wg := sync.WaitGroup{}
	for x, node := range ncfg {
		wg.Add(1)
		go func() {
			defer wg.Done()
			level := slog.LevelInfo
			if x == 0 {
				level = slog.LevelDebug
			}
			err := core.Start(ccfg, node, level)
			if err != nil {
				panic(err)
			}
		}()
	}
	wg.Wait()
}
