package state

import (
	"context"
	"crypto/ed25519"
	"log/slog"
	"sync/atomic"
)

type NyModule interface {
	Init(s *State) error
	Cleanup(s *State) error
}

// State access must be done only on a single Goroutine
type State struct {
	*Env
	TrustedNodes map[NodeId]ed25519.PublicKey
	Modules      map[string]NyModule
	Neighbours   []*Neighbour
}

// Env can be read from any Goroutine
type Env struct {
	DispatchChannel chan<- func(s *State) error
	CentralCfg
	LocalCfg
	Context      context.Context
	Cancel       context.CancelCauseFunc
	Log          *slog.Logger
	Updating     atomic.Bool
	ConfigPath   string
	IPForwardOff bool
}
