package state

import (
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
)

var namePattern, _ = regexp.Compile("^[0-9a-z._-]+$")

func PathValidator(s string) error {
	_, err := os.Stat(path.Dir(s))
	if err != nil {
		return err
	}
	_, err = filepath.Abs(s)
	return err
}

func NameValidator(s string) error {
	if !namePattern.MatchString(s) {
		return fmt.Errorf("%s is not a valid name, must match pattern %s", s, namePattern.String())
	}
	if len(s) > 100 {
		return fmt.Errorf("len(\"%s\") = %d > 100 is too long", s, len(s))
	}
	return nil
}

func BindValidator(s string) error {
	_, err := netip.ParseAddrPort(s)
	return err
}

func NodeConfigValidator(node *NodeCfg) error {
	err := NameValidator(string(node.Id))
	if err != nil {
		return err
	}
	if !node.ProbeBind.IsValid() {
		return fmt.Errorf("node.ProbeBind is invalid")
	}
	if !node.DpBind.IsValid() {
		return fmt.Errorf("node.DpBind is invalid")
	}
	if !node.CtlBind.IsValid() {
		return fmt.Errorf("node.CtlBind is invalid")
	}
	return nil
}

func CentralConfigValidator(cfg *CentralCfg) error {
	for _, node := range cfg.Nodes {
		err := NameValidator(string(node.Id))
		if err != nil {
			return err
		}
	}
	nodeRel := make([]Pair[Node, Node], 0)
	for _, edge := range cfg.Edges {
		if slices.Contains(nodeRel, edge) {
			return fmt.Errorf("duplicate edge found: %s, %s", edge.V1, edge.V2)
		}
		if !slices.ContainsFunc(cfg.Nodes, func(cfg PubNodeCfg) bool {
			return cfg.Id == edge.V1
		}) {
			return fmt.Errorf("node %s not defined", edge.V1)
		}
		if !slices.ContainsFunc(cfg.Nodes, func(cfg PubNodeCfg) bool {
			return cfg.Id == edge.V2
		}) {
			return fmt.Errorf("node %s not defined", edge.V2)
		}
		nodeRel = append(nodeRel, edge)
		nodeRel = append(nodeRel, Pair[Node, Node]{edge.V2, edge.V1})
	}
	return nil
}
