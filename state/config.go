package state

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

type NodeCfg struct {
	Id       NodeId
	PubKey   NyPublicKey
	Address  *netip.Addr `yaml:",omitempty"`
	Services []ServiceId `yaml:",omitempty"`
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

type CentralCfg struct {
	Dist      *DistributionCfg `yaml:",omitempty"`
	Routers   []RouterCfg
	Clients   []ClientCfg
	Graph     []string
	Timestamp int64
	Services  map[ServiceId]netip.Prefix
}

func (c *CentralCfg) RegisterService(svcId ServiceId, prefix netip.Prefix) ServiceId {
	if c.Services == nil {
		c.Services = make(map[ServiceId]netip.Prefix)
	}
	c.Services[svcId] = prefix
	return svcId
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

// TODO: Allow node to be configured to NOT be a router
// LocalCfg represents local node-level configuration
type LocalCfg struct {
	// Node Private Key
	Key NyPrivateKey
	Id  NodeId
	// Address that the data plane can be accessed by
	Port             uint16
	DisableRouting   bool
	UseSystemRouting bool
	NoNetConfigure   bool `yaml:",omitempty"`
	InterfaceName    string
	LogPath          string
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

func (e *Env) GetPeers() []NodeId {
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
		if edge.V1 == e.Id {
			neighNode = edge.V2
		}
		if edge.V2 == e.Id {
			neighNode = edge.V1
		}
		if neighNode != e.Id && neighNode != "" {
			nodes = append(nodes, neighNode)
		}
	}
	return nodes
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

func (e *CentralCfg) GetSvcPrefix(svc ServiceId) netip.Prefix {
	return e.Services[svc]
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
