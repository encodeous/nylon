//go:build integration

package core

import (
	"fmt"
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
	return factory.NewRuntime(n)
}
