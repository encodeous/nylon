//go:build integration

package core

import (
	"fmt"

	"github.com/encodeous/polyamide-wireguard-go/adapter"
)

// The integration environment supplies a complete runtime. Nylon does not know
// which protocol, sockets, or bind implementation that transport uses.
type virtualRuntimeFactory interface {
	NewRuntime(*Nylon) (Runtime, error)
}

func newDefaultRuntime(n *Nylon) (Runtime, error) {
	factory, ok := n.AuxConfig["vnet"].(virtualRuntimeFactory)
	if !ok {
		return Runtime{}, fmt.Errorf("expected aux config vnet runtime factory")
	}
	rt, err := factory.NewRuntime(n)
	if err != nil {
		return Runtime{}, err
	}
	// Tests use the WireGuard transport's UAPI without a socket.
	if len(rt.Transports) != 0 {
		if wg, ok := rt.Transports[0].(*adapter.Transport); ok {
			n.wireGuard = &legacyWireGuard{transport: wg}
		}
	}
	return rt, nil
}
