package state

import (
	"context"
	"crypto/ed25519"
	"golang.org/x/sync/semaphore"
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
	Semaphore       *semaphore.Weighted
	DispatchChannel chan<- func(s *State) error
	CentralCfg
	LocalCfg
	Context    context.Context
	Cancel     context.CancelCauseFunc
	Log        *slog.Logger
	AuxConfig  map[string]any
	Updating   atomic.Bool
	Stopping   atomic.Bool
	Started    atomic.Bool
	ConfigPath string
}
