package state

import (
	"fmt"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
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

func PortValidator(s string) error {
	_, err := strconv.ParseUint(s, 10, 16)
	return err
}

func AddrValidator(s string) error {
	_, err := netip.ParseAddr(s)
	return err
}

func NodeConfigValidator(node *NodeCfg) error {
	err := NameValidator(string(node.Id))
	if err != nil {
		return err
	}
	if node.DpPort == 0 {
		return fmt.Errorf("node.DpPort is invalid")
	}
	if !node.CtlBind.IsValid() {
		return fmt.Errorf("node.CtlBind is invalid")
	}
	return nil
}

func CentralConfigValidator(cfg *CentralCfg) error {
	nodes := make([]string, 0)
	for _, node := range cfg.Nodes {
		err := NameValidator(string(node.Id))
		if err != nil {
			return err
		}
		nodes = append(nodes, string(node.Id))
	}
	_, err := ParseGraph(cfg.Graph, nodes)
	if err != nil {
		return err
	}
	return nil
}
