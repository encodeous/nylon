package core

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"gopkg.in/yaml.v3"
	"log/slog"
	"math/big"
	"time"
)

func Start() error {
	_, nodeKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}

	certTemplate := x509.Certificate{
		PublicKey: nodeKey.Public(),
		Subject: pkix.Name{
			CommonName: "dummyNode",
		},
		IsCA:         false,
		SubjectKeyId: nil,
		NotBefore:    time.Now(),
		NotAfter:     time.Now().AddDate(10, 0, 0),
		SerialNumber: big.NewInt(time.Now().Unix()),
	}

	ss, err := x509.CreateCertificate(rand.Reader, &certTemplate, &certTemplate, certTemplate.PublicKey, nodeKey)
	if err != nil {
		return err
	}

	dpKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	mockNode := NodeCfg{
		Id:    "currentNode",
		Key:   EdPrivateKey(nodeKey),
		DpKey: (*EcPrivateKey)(dpKey),
		Cert:  Cert(ss),
	}

	cfg, err := yaml.Marshal(mockNode)

	slog.Info(string(cfg))

	cfg = nil

	err = yaml.Unmarshal(cfg, &mockNode)

	cfg, err = yaml.Marshal(mockNode)

	slog.Info(string(cfg))

	return nil
}

func MainLoop(env MainEnv, genv GlobalEnv, dispatch <-chan func(env MainEnv)) error {
	log := genv.Logger.WithGroup("main")
	log.Info("loop start")
	for {
		select {
		case <-env.Context.Done():
			goto endLoop
		case fun := <-dispatch:
			log.Debug("dispatch start")
			start := time.Now()
			fun(env)
			elapsed := time.Since(start)
			log.Debug("dispatch done", "elapsed", elapsed)
		}
	}
endLoop:
	log.Info("loop end")
	return nil
}
