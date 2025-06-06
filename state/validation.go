package state

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
)

var namePattern, _ = regexp.Compile("^[0-9a-z._/-]+$")

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

func PortValidator(s string) error {
	_, err := strconv.ParseUint(s, 10, 16)
	return err
}

func NodeConfigValidator(node *LocalCfg) error {
	err := NameValidator(string(node.Id))
	if err != nil {
		return err
	}
	if node.Port == 0 {
		return fmt.Errorf("port must be greater than 0")
	}
	if node.Key == [32]byte{} {
		return fmt.Errorf("private key must not be empty")
	}
	if node.InterfaceName != "" {
		err = NameValidator(node.InterfaceName)
		if err != nil {
			return fmt.Errorf("interface name is invalid: %v", err)
		}
	}
	return nil
}

func CentralConfigValidator(cfg *CentralCfg) error {
	nodes := make([]string, 0)
	for _, node := range cfg.Routers {
		err := NameValidator(string(node.Id))
		if err != nil {
			return err
		}
		if slices.Contains(nodes, string(node.Id)) {
			return fmt.Errorf("duplicate router id %s", node.Id)
		}
		nodes = append(nodes, string(node.Id))
	}
	for _, node := range cfg.Clients {
		err := NameValidator(string(node.Id))
		if err != nil {
			return err
		}
		if slices.Contains(nodes, string(node.Id)) {
			return fmt.Errorf("duplicate client id %s", node.Id)
		}
		nodes = append(nodes, string(node.Id))
	}
	_, err := ParseGraph(cfg.Graph, nodes)
	if err != nil {
		return err
	}

	//prefixes := make([]netip.Prefix, 0)

	// ensure prefixes do not overlap
	//for _, node := range cfg.Routers {
	//	for _, prefix := range node.Prefixes {
	//		for _, oPrefix := range prefixes {
	//			if oPrefix.Overlaps(prefix) {
	//				return fmt.Errorf("node %s's prefix %s overlaps with existing prefix %s", node, oPrefix.String(), prefix.String())
	//			}
	//		}
	//		prefixes = append(prefixes, prefix)
	//	}
	//}

	if cfg.Dist != nil {
		// validate repos
		for _, repo := range cfg.Dist.Repos {
			_, err := url.Parse(repo)
			if err != nil {
				return err
			}
		}
	}

	for domain, node := range cfg.Hosts {
		if !slices.Contains(nodes, node) {
			return fmt.Errorf("destination node %s not found for %s. nodes: %v", node, domain, nodes)
		}
		if slices.Contains(nodes, domain) {
			return fmt.Errorf("domain name %s cannot be the same name as node %s", domain, node)
		}
	}

	return nil
}
