package state

import (
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"log/slog"
	"time"
)

type Pair[Ty1, Ty2 any] struct {
	V1 Ty1
	V2 Ty2
}

type NyModule interface {
	Init(s *State) error
}

type State struct {
	*Env
	TrustedNodes map[Node]ed25519.PublicKey
	Modules      map[string]NyModule
}

type Env struct {
	DispatchChannel chan<- func(s *State) error
	LinkChannel     chan<- CtlLink
	CentralCfg
	NodeCfg
	Context context.Context
	Cancel  context.CancelCauseFunc
	Log     *slog.Logger
}

// NodeCfg represents local node-level configuration
type NodeCfg struct {
	// Node Private Key
	Key EdPrivateKey
	// Data plane (WireGuard) Private key
	DpKey *EcPrivateKey
	// x509 certificate signed by the root CA
	Cert Cert
	Id   Node
	// Address and port that the control plane listens on
	CtlAddr string
}

func (n NodeCfg) GeneratePubCfg() PubNodeCfg {
	cfg := PubNodeCfg{
		Id:      n.Id,
		CtlAddr: n.CtlAddr,
	}
	if n.DpKey != nil {
		cfg.DpPubKey = (*EcPublicKey)(((*ecdh.PrivateKey)(n.DpKey).Public()).(*ecdh.PublicKey))
	}
	if n.Key != nil {
		cfg.PubKey = EdPublicKey(((ed25519.PrivateKey)(n.Key).Public()).(ed25519.PublicKey))
	}
	return cfg
}

// PubNodeCfg represents a central representation of a node
type PubNodeCfg struct {
	Id       Node
	PubKey   EdPublicKey
	DpPubKey *EcPublicKey
	CtlAddr  string
}

type CentralCfg struct {
	// the public key of the root CA
	RootCa  Cert
	Nodes   []PubNodeCfg
	Edges   []Pair[Node, Node]
	Version uint64
}

// Dispatch Dispatches the function to run on the main thread without waiting for it to complete
func (e Env) Dispatch(fun func(*State) error) {
	e.DispatchChannel <- fun
}

// DispatchWait Dispatches the function to run on the main thread and wait for it to complete
func (e Env) DispatchWait(fun func(*State) (any, error)) (any, error) {
	ret := make(chan Pair[any, error])
	e.DispatchChannel <- func(s *State) error {
		res, err := fun(s)
		ret <- Pair[any, error]{res, err}
		return err
	}
	select {
	case res := <-ret:
		return res.V1, res.V2
	case <-e.Context.Done():
		return nil, e.Context.Err()
	}
}

func (e Env) scheduledTask(fun func(*State) error, delay time.Duration) {
	time.Sleep(delay)
	e.Dispatch(fun)
}

func (e Env) ScheduleTask(fun func(*State) error, delay time.Duration) {
	go e.scheduledTask(fun, delay)
}

func (e Env) repeatedTask(fun func(*State) error, delay time.Duration) {
	for e.Context.Err() == nil {
		e.Dispatch(fun)
		time.Sleep(delay)
	}
}

func (e Env) RepeatTask(fun func(*State) error, delay time.Duration) {
	go e.repeatedTask(fun, delay)
}
