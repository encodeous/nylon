//go:generate protoc -I . --go_out=. ./protocol/nylon.proto
package main

import (
	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/mock"
	"log/slog"
	"sync"
	"time"
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
//		WgKey: (*state.EcPrivateKey)(dpKey),
//		Cert:  state.Cert(ss),
//	}
//
//	mockPubNode := mockNode.GeneratePubCfg()
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

func main() {
	ccfg, ncfg, err := mock.MockCfg()

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
	go func() {
		wg.Add(1)
		defer wg.Done()
		// weight changer
		for {
			//slog.Info("Changing Weights...")
			//for _, edge := range ccfg.MockWeights {
			//	met := edge.V3
			//	res := rand.Int()%4 == 0
			//	if res && *met > 1 {
			//		*met--
			//	} else {
			//		*met += time.Millisecond * time.Duration(rand.Int()%15+5)
			//	}
			//	if *met == 0 {
			//		*met++
			//	}
			//	slog.Info("changed", "a", edge.V1, "b", edge.V2, "met", *edge.V3)
			//}
			time.Sleep(time.Second * 10)
		}
	}()
	wg.Wait()
}
