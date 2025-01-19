package state

import (
	"errors"
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
