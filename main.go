//go:generate protoc -I . --go_out=. ./protocol/nylon.proto
package main

import (
	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/mock"
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
	wg.Wait()
}
