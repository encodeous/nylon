package core

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"sync"
)

type MainEnv struct {
	Context      context.Context
	TrustedNodes map[Node]ed25519.PublicKey
}

type GlobalEnv struct {
	DispatchChannel chan<- func(env MainEnv) error
	Logger          slog.Logger
}

// NodeCfg represents local node-level configuration
type NodeCfg struct {
	// Node Private Key
	Key EdPrivateKey
	// Data plane (WireGuard) Private key
	DpKey *EcPrivateKey
	// x509 certificate signed by the root CA
	Cert Cert
	Id   string
}

// PubNodeCfg represents a central representation of a node
type PubNodeCfg struct {
	Id       string
	PubKey   EdPublicKey
	DpPubKey EcPublicKey
}

type CentralCfg struct {
	// the public key of the root CA
	RootCa  Cert
	Nodes   []PubNodeCfg
	Version uint64
}

// Dispatch Dispatches the function to run on the main thread without waiting for it to complete
func (env GlobalEnv) Dispatch(fun func(env MainEnv) error) {
	env.DispatchChannel <- fun
}

// DispatchWait Dispatches the function to run on the main thread and wait for it to complete
func (env GlobalEnv) DispatchWait(fun func(env MainEnv) error) {
	wg := sync.WaitGroup{}
	wg.Add(1)
	env.DispatchChannel <- func(env MainEnv) error {
		defer wg.Done()
		return fun(env)
	}
	wg.Wait()
}
