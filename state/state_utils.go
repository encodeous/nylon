package state

import (
	"errors"
	"net/netip"
	"slices"
)

func (e Env) GetPubNodeCfg(node Node) (PubNodeCfg, error) {
	idx := slices.IndexFunc(e.Nodes, func(cfg PubNodeCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		return PubNodeCfg{}, errors.New("node " + string(node) + " not found")
	}

	return e.Nodes[idx], nil
}

func (s State) GetPubNodeCfg(node Node) (PubNodeCfg, error) {
	return s.Env.GetPubNodeCfg(node)
}

type Pair[Ty1, Ty2 any] struct {
	V1 Ty1
	V2 Ty2
}
type Triple[Ty1, Ty2, Ty3 any] struct {
	V1 Ty1
	V2 Ty2
	V3 Ty3
}

func RepAddr(bind netip.AddrPort, addr netip.Addr) netip.AddrPort {
	return netip.AddrPortFrom(addr, bind.Port())
}
