package state

import (
	"cmp"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"

	"github.com/cilium/cilium/pkg/ip"
)

type NodeCfg struct {
	Id        NodeId
	PubKey    NyPublicKey
	Addresses []netip.Addr   `yaml:",omitempty"`
	Prefixes  []netip.Prefix `yaml:",omitempty"`
}

// RouterCfg represents a central representation of a node that can route
type RouterCfg struct {
	NodeCfg   `yaml:",inline"`
	Endpoints []netip.AddrPort
}
type ClientCfg struct {
	NodeCfg `yaml:",inline"`
}

type DistributionCfg struct {
	Key   NyPublicKey // also used as shared secret, so, although its "public", it's not a good idea to share it.
	Repos []string
}

type LocalDistributionCfg struct {
	Key NyPublicKey
	Url string
}

type CentralCfg struct {
	Dist       *DistributionCfg `yaml:",omitempty"`
	Routers    []RouterCfg
	Clients    []ClientCfg
	Graph      []string
	Timestamp  int64
	ExcludeIPs []netip.Prefix `yaml:"exclude_ips,omitempty"` // split tunnel, default excluded ip ranges for the whole network, if empty, all advertised prefixes will be included
}

// LocalCfg represents local node-level configuration
type LocalCfg struct {
	// Node Private Key
	Key              NyPrivateKey
	Id               NodeId                // unique id for this node
	Port             uint16                // Address that the data plane can be accessed by
	Dist             *LocalDistributionCfg `yaml:",omitempty"`                   // distribution configuration
	UseSystemRouting bool                  `yaml:"use_system_routing,omitempty"` // all packets from peers will come out of the TUN interface
	NoNetConfigure   bool                  `yaml:"no_net_configure,omitempty"`   // do not configure system networking at all
	DnsResolvers     []string              `yaml:"dns_resolvers,omitempty"`      // dns resolvers used by nylon, currently only for config repo
	InterfaceName    string                `yaml:"interface_name,omitempty"`     // the name of the nylon interface
	LogPath          string                `yaml:"log_path,omitempty"`           // if not empty, nylon will write to this file
	IncludeIPs       []netip.Prefix        `yaml:"include_ips,omitempty"`        // split tunnel, subtracts from centrally excluded ip ranges
	ExcludeIPs       []netip.Prefix        `yaml:"exclude_ips,omitempty"`        // split tunnel, adds to the centrally excluded ip ranges
	PreUp            []string              `yaml:"pre_up,omitempty"`             // a list of commands executed in order before the nylon interface is brought up
	PreDown          []string              `yaml:"pre_down,omitempty"`           // a list of commands executed in order before the nylon interface is brought down
	PostUp           []string              `yaml:"post_up,omitempty"`            // a list of commands executed in order after the nylon interface is brought up
	PostDown         []string              `yaml:"post_down,omitempty"`          // a list of commands executed in order after the nylon interface is brought down
}

// GetPrefixes returns all unique prefixes from all nodes
func (c *CentralCfg) GetPrefixes() []netip.Prefix {
	prefixMap := make(map[netip.Prefix]bool)

	// Collect from routers
	for _, router := range c.Routers {
		for _, prefix := range router.Prefixes {
			prefixMap[prefix] = true
		}
	}

	// Collect from clients
	for _, client := range c.Clients {
		for _, prefix := range client.Prefixes {
			prefixMap[prefix] = true
		}
	}

	// Convert to slice
	prefixes := make([]netip.Prefix, 0, len(prefixMap))
	for prefix := range prefixMap {
		prefixes = append(prefixes, prefix)
	}

	return prefixes
}

func (c *CentralCfg) GetNodes() []NodeCfg {
	nodes := make([]NodeCfg, 0)
	for _, n := range c.Routers {
		nodes = append(nodes, n.NodeCfg)
	}
	for _, n := range c.Clients {
		nodes = append(nodes, n.NodeCfg)
	}
	return nodes
}

func parseSymbolList(s string, validSymbols []string) ([]string, error) {
	spl := strings.Split(strings.TrimSpace(s), ",")
	line := make([]string, 0)
	for _, s := range spl {
		x := strings.TrimSpace(s)
		if x == "" {
			continue
		}
		if !slices.Contains(validSymbols, x) {
			return nil, fmt.Errorf(`%s is not a valid node/group`, x)
		}
		line = append(line, x)
	}
	if len(line) == 0 {
		return nil, fmt.Errorf(`node/group list must not be empty`)
	}
	slices.Sort(line)
	return line, nil
}

func toIPNets(prefixes []netip.Prefix) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(prefixes))
	for _, p := range prefixes {
		if p.IsValid() {
			nets = append(nets, &net.IPNet{
				IP:   p.Addr().AsSlice(),
				Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen()),
			})
		}
	}
	return nets
}

func fromIPNets(nets []*net.IPNet) []netip.Prefix {
	output := make([]netip.Prefix, 0, len(nets))
	for _, n := range nets {
		if addr, ok := netip.AddrFromSlice(n.IP); ok {
			ones, _ := n.Mask.Size()
			output = append(output, netip.PrefixFrom(addr.Unmap(), ones))
		}
	}
	return output
}

func SubtractPrefix(includesPrefix, excludesPrefix []netip.Prefix) []netip.Prefix {
	result := ip.RemoveCIDRs(toIPNets(includesPrefix), toIPNets(excludesPrefix))
	ipv4, ipv6 := ip.CoalesceCIDRs(result)
	return fromIPNets(append(ipv4, ipv6...))
}

func CoalescePrefix(prefixes []netip.Prefix) []netip.Prefix {
	ipv4, ipv6 := ip.CoalesceCIDRs(toIPNets(prefixes))
	return fromIPNets(append(ipv4, ipv6...))
}

/*
ParseGraph Graph syntax is something like this:

Group1 = node1, node2, node3

Group2 = node4, node5

Group1, Group2, OtherNode // Group1, Group2, OtherNode will all be interconnected, but not within Group1 or Group2

Group1, Group1 // every node is connected to every other node

node8, node9 // node8 and node9 will be connected

graph represents the above graph
nodes represents a set of unique terminal nodes that the graph will evaluate down to
*/
func ParseGraph(graph []string, nodes []string) ([]Pair[NodeId, NodeId], error) {
	// why can't we just have unordered_set<Pair<NodeId, NodeId>> :(

	parsedPairings := make([]Pair[string, string], 0)

	groups := make(map[string][]string)

	symbols := slices.Clone(nodes)

	// pass 0, collect all symbols

	for _, line := range graph {
		line = strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(line, "=") {
			// group definition
			spl := strings.Split(line, "=")
			if len(spl) != 2 {
				return nil, fmt.Errorf("invalid graph: %s. group definition must contain one '='", line)
			}
			grp := strings.TrimSpace(spl[0])
			if slices.Contains(nodes, grp) {
				return nil, fmt.Errorf("group name must not be a node name: %s", grp)
			}
			symbols = append(symbols, grp)
		}
	}
	slices.Sort(symbols)
	symbols = slices.Compact(symbols)

	// used for topological sorting
	// map: group -> []<groups that the node depends on>
	topo := make(map[string][]string)
	expansion := make(map[string][]string)

	// pass 1, parse graph
	for _, line := range graph {
		line = strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(line, "=") {
			spl := strings.Split(line, "=")
			grp := strings.TrimSpace(spl[0])
			if _, ok := groups[grp]; ok {
				return nil, fmt.Errorf("duplicate group name: %s", grp)
			}
			lst, err := parseSymbolList(spl[1], symbols)
			if err != nil {
				return nil, err
			}
			// track dependencies
			deps := make([]string, 0)
			for _, l := range lst {
				if !slices.Contains(nodes, l) {
					// depends on a group
					deps = append(deps, l)
				} else {
					expansion[grp] = append(expansion[grp], l)
				}
			}
			slices.Sort(deps)
			deps = slices.Compact(deps)

			topo[grp] = deps
			groups[grp] = lst
		} else {
			names, err := parseSymbolList(line, symbols)
			if err != nil {
				return nil, err
			}
			if len(names) < 2 {
				return nil, fmt.Errorf("invalid pairing, %v", names)
			}
			interconnectNodes := make([]NodeId, 0)
			for _, name := range names {
				for _, node := range interconnectNodes {
					parsedPairings = append(parsedPairings, MakeSortedPair(string(node), name))
				}
				interconnectNodes = append(interconnectNodes, NodeId(name))
			}
			SortPairs(parsedPairings)
			parsedPairings = slices.Compact(parsedPairings)
		}
	}

	// pass 2, expand group names
	// just topological sorting
	for len(topo) > 0 {
		// find free group
		var group string
		for k, v := range topo {
			if len(v) == 0 {
				group = k
				break
			}
		}
		if group == "" {
			cycleNodes := make([]string, 0)
			for node := range topo {
				cycleNodes = append(cycleNodes, node)
			}
			slices.Sort(cycleNodes)
			return nil, fmt.Errorf("cycle detected in graph: %v", cycleNodes)
		}
		delete(topo, group)

		// remove and expand the group for every dependent
		for k, deps := range topo {
			if slices.Contains(deps, group) {
				// remove it from the group and copy the value to the expansion
				expansion[k] = append(expansion[k], expansion[group]...)
				slices.Sort(expansion[k])
				expansion[k] = slices.Compact(expansion[k])

				// remove group from deps
				x := 0
				for _, dep := range deps {
					if dep == group {
						// remove
					} else {
						deps[x] = dep
						x++
					}
				}
				deps = deps[:x]
				topo[k] = deps
			}
		}
	}

	// pass 3, rewrite pairings
	pairings := make([]Pair[NodeId, NodeId], 0)
	for _, pair := range parsedPairings {
		x := make([]NodeId, 0)
		if slices.Contains(nodes, pair.V1) {
			x = append(x, NodeId(pair.V1))
		} else {
			for _, exp := range expansion[pair.V1] {
				x = append(x, NodeId(exp))
			}
		}
		y := make([]NodeId, 0)
		if slices.Contains(nodes, pair.V2) {
			y = append(y, NodeId(pair.V2))
		} else {
			for _, exp := range expansion[pair.V2] {
				y = append(y, NodeId(exp))
			}
		}
		for _, x1 := range x {
			for _, y1 := range y {
				if x1 != y1 {
					pairings = append(pairings, MakeSortedPair(x1, y1))
				}
			}
		}
		SortPairs(pairings)
		pairings = slices.Compact(pairings)
	}
	return pairings, nil
}

func MakeSortedPair[T cmp.Ordered](a, b T) Pair[T, T] {
	if a < b {
		return Pair[T, T]{a, b}
	} else {
		return Pair[T, T]{b, a}
	}
}

func (e *CentralCfg) GetPeers(curId NodeId) []NodeId {
	allNodes := make([]string, 0)
	for _, node := range e.Routers {
		allNodes = append(allNodes, string(node.Id))
	}
	for _, node := range e.Clients {
		allNodes = append(allNodes, string(node.Id))
	}
	graph, err := ParseGraph(e.Graph, allNodes)
	if err != nil {
		panic(err)
	}
	nodes := make([]NodeId, 0)
	for _, edge := range graph {
		var neighNode NodeId
		if edge.V1 == curId {
			neighNode = edge.V2
		}
		if edge.V2 == curId {
			neighNode = edge.V1
		}
		if neighNode != curId && neighNode != "" {
			nodes = append(nodes, neighNode)
		}
	}
	return nodes
}

func (e *CentralCfg) FindNodeBy(pkey NyPublicKey) *NodeId {
	for _, n := range e.Routers {
		if n.PubKey == pkey {
			return &n.Id
		}
	}
	for _, n := range e.Clients {
		if n.PubKey == pkey {
			return &n.Id
		}
	}
	return nil
}

func ExpandCentralConfig(cfg *CentralCfg) {
	// compatibility & convenience: advertise address as a host address (/32 or /128)
	for idx, node := range cfg.Routers {
		for _, addr := range node.Addresses {
			node.Prefixes = append([]netip.Prefix{AddrToPrefix(addr)}, node.Prefixes...)
		}
		cfg.Routers[idx] = node
	}
	for idx, node := range cfg.Clients {
		for _, addr := range node.Addresses {
			node.Prefixes = append([]netip.Prefix{AddrToPrefix(addr)}, node.Prefixes...)
		}
		cfg.Clients[idx] = node
	}
}

func (e *CentralCfg) IsRouter(node NodeId) bool {
	idx := slices.IndexFunc(e.Routers, func(cfg RouterCfg) bool {
		return cfg.Id == node
	})
	return idx != -1
}

func (e *CentralCfg) IsClient(node NodeId) bool {
	idx := slices.IndexFunc(e.Clients, func(cfg ClientCfg) bool {
		return cfg.Id == node
	})
	return idx != -1
}

func (e *CentralCfg) IsNode(node NodeId) bool {
	return e.IsRouter(node) || e.IsClient(node)
}

func (e *CentralCfg) GetNode(node NodeId) NodeCfg {
	val := e.TryGetNode(node)
	if val == nil {
		panic("node " + string(node) + " not found")
	}
	return *val
}

func (e *CentralCfg) TryGetNode(node NodeId) *NodeCfg {
	idx := slices.IndexFunc(e.Routers, func(cfg RouterCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		idx = slices.IndexFunc(e.Clients, func(cfg ClientCfg) bool {
			return cfg.Id == node
		})
		if idx == -1 {
			return nil
		}
		return &e.Clients[idx].NodeCfg
	}
	return &e.Routers[idx].NodeCfg
}

func (e *CentralCfg) GetRouter(node NodeId) RouterCfg {
	idx := slices.IndexFunc(e.Routers, func(cfg RouterCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		panic("router " + string(node) + " not found")
	}

	return e.Routers[idx]
}

func (e *CentralCfg) GetClient(node NodeId) ClientCfg {
	idx := slices.IndexFunc(e.Clients, func(cfg ClientCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		panic("client " + string(node) + " not found")
	}

	return e.Clients[idx]
}
