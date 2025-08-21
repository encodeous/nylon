package state

import (
	"context"
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
	Modules map[string]NyModule
	RouterState
}

// Env can be read from any Goroutine
type Env struct {
	DispatchChannel chan func(s *State) error
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
