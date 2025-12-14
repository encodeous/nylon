package state

import (
	"fmt"
	"net/netip"
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
	if node.Dist != nil {
		_, err := url.Parse(node.Dist.Url)
		if err != nil {
			return err
		}
	}
	if len(node.DnsResolvers) != 0 {
		for _, resolver := range node.DnsResolvers {
			if _, err := netip.ParseAddrPort(resolver); err != nil {
				return fmt.Errorf("dns resolver %s is not a valid ip:port: %v", resolver, err)
			}
		}
	}
	return nil
}

func AddrToPrefix(addr netip.Addr) netip.Prefix {
	res, err := addr.Prefix(addr.BitLen())
	if err != nil {
		panic(err)
	}
	return res
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

	// ensure each node contains unique prefixes (anycast routing allows duplicate prefixes across nodes)
	for _, router := range cfg.Routers {
		routerPrefixes := make(map[netip.Prefix]struct{})
		for _, p := range router.Prefixes {
			if _, ok := routerPrefixes[p]; ok {
				return fmt.Errorf("router %s has duplicate prefix %s", router.Id, p)
			}
			routerPrefixes[p] = struct{}{}
		}
		for _, peer := range cfg.GetPeers(router.Id) {
			if cfg.IsClient(peer) {
				client := cfg.GetClient(peer)
				for _, cp := range client.Prefixes {
					if _, ok := routerPrefixes[cp]; ok {
						return fmt.Errorf("router %s has duplicate prefix %s (provided by client %s)", router.Id, cp, client.Id)
					}
					routerPrefixes[cp] = struct{}{}
				}
			}
		}
	}

	if cfg.Dist != nil {
		// validate repos
		for _, repo := range cfg.Dist.Repos {
			_, err := url.Parse(repo)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
