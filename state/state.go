package state

import (
	"context"
	"crypto/ed25519"
	"github.com/encodeous/polyamide/conn"
	"log/slog"
	"net/netip"
	"slices"
)

type NyModule interface {
	Init(s *State) error
	Cleanup(s *State) error
}

// State access must be done only on a single Goroutine
type State struct {
	*Env
	TrustedNodes map[Node]ed25519.PublicKey
	Modules      map[string]NyModule
	Neighbours   []*Neighbour
}

func (s *State) GetNeighbour(node Node) *Neighbour {
	nIdx := slices.IndexFunc(s.Neighbours, func(neighbour *Neighbour) bool {
		return neighbour.Id == node
	})
	if nIdx == -1 {
		return nil
	}
	return s.Neighbours[nIdx]
}

// Env can be read from any Goroutine
type Env struct {
	DispatchChannel chan<- func(s *State) error
	CentralCfg
	NodeCfg
	Context context.Context
	Cancel  context.CancelCauseFunc
	Log     *slog.Logger
}

type NetworkEndpoint struct {
	RemoteInit bool
	WgEndpoint conn.Endpoint
	Ep         netip.AddrPort
}

func (ep *NetworkEndpoint) GetWgEndpoint() conn.Endpoint {
	if ep.WgEndpoint == nil || ep.WgEndpoint.DstToString() != ep.Ep.String() {
		ep.WgEndpoint = &conn.StdNetEndpoint{AddrPort: ep.Ep}
	}
	return ep.WgEndpoint
}
