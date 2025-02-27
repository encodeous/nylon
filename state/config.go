package state

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strings"
)

var NodeConfigPath = "./node.yaml"
var CentralConfigPath = "./central.yaml"
var CentralKeyPath = "./central.key"

type NodeCfg struct {
	Id       NodeId
	PubKey   NyPublicKey
	Prefixes []netip.Prefix
}

// RouterCfg represents a central representation of a node that can route
type RouterCfg struct {
	NodeCfg   `yaml:",inline"`
	Endpoints []netip.AddrPort
}
type ClientCfg struct {
	NodeCfg `yaml:",inline"`
}
type CentralCfg struct {
	// the public key of the root CA
	RootKey   NyPublicKey
	Repos     []string
	Routers   []RouterCfg
	Clients   []ClientCfg
	Graph     []string
	Timestamp int64
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
	Port            uint16
	NoNetConfigure  bool
	AllowedPrefixes []netip.Prefix
}

func (n LocalCfg) NewRouterCfg(extIp netip.Addr, port uint16, nylonIp netip.Prefix) RouterCfg {
	extDp := netip.AddrPortFrom(extIp, port)
	cfg := RouterCfg{
		NodeCfg: NodeCfg{
			Id: n.Id,
			Prefixes: []netip.Prefix{
				nylonIp,
			},
		},
		Endpoints: []netip.AddrPort{
			extDp,
		},
	}
	cfg.PubKey = n.Key.XPubkey()
	return cfg
}

func parseSymbolList(s string, validSymbols []string) ([]string, error) {
	spl := strings.Split(strings.TrimSpace(s), ",")
	line := make([]string, 0)
	for _, s := range spl {
		x := strings.TrimSpace(s)
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
*/
func ParseGraph(graph []string, nodes []string) ([]Pair[NodeId, NodeId], error) {
	// why can't we just have unordered_set<Pair<NodeId, NodeId>> :(

	pairings := make([]Pair[NodeId, NodeId], 0)

	groups := make(map[string][]string)

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
			if _, ok := groups[grp]; ok {
				return nil, fmt.Errorf("duplicate group name: %s", grp)
			}
			lst, err := parseSymbolList(spl[1], append(nodes, slices.Collect(maps.Keys(groups))...))
			if err != nil {
				return nil, err
			}
			groups[grp] = lst
		} else {
			names, err := parseSymbolList(line, append(nodes, slices.Collect(maps.Keys(groups))...))
			if err != nil {
				return nil, err
			}
			interconnectNodes := make([]NodeId, 0)
			for _, name := range names {
				if slices.Contains(nodes, name) {
					for _, node := range interconnectNodes {
						if node != NodeId(name) {
							pairings = append(pairings, makeSortedPair(node, NodeId(name)))
						}
					}
					interconnectNodes = append(interconnectNodes, NodeId(name))
				} else {
					for _, node := range interconnectNodes {
						for _, grpNode := range groups[name] {
							if node != NodeId(grpNode) {
								pairings = append(pairings, makeSortedPair(node, NodeId(grpNode)))
							}
						}
					}
					for _, grpNode := range groups[name] {
						interconnectNodes = append(interconnectNodes, NodeId(grpNode))
					}
				}
			}
			sort.Slice(pairings, func(i, j int) bool {
				x := strings.Compare(string(pairings[i].V1), string(pairings[j].V1))
				y := strings.Compare(string(pairings[i].V2), string(pairings[j].V2))
				return x < 0 || x == 0 && y < 0
			})
			pairings = slices.Compact(pairings)
		}
	}
	return pairings, nil
}

func makeSortedPair(a NodeId, b NodeId) Pair[NodeId, NodeId] {
	if strings.Compare(string(a), string(b)) < 0 {
		return Pair[NodeId, NodeId]{a, b}
	} else {
		return Pair[NodeId, NodeId]{b, a}
	}
}

func (e *Env) FindNodeBy(pkey NyPublicKey) *NodeId {
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

func (e *Env) IsRouter(node NodeId) bool {
	idx := slices.IndexFunc(e.Routers, func(cfg RouterCfg) bool {
		return cfg.Id == node
	})
	return idx != -1
}

func (e *Env) IsClient(node NodeId) bool {
	idx := slices.IndexFunc(e.Clients, func(cfg ClientCfg) bool {
		return cfg.Id == node
	})
	return idx != -1
}

func (e *Env) IsNode(node NodeId) bool {
	return e.IsRouter(node) || e.IsClient(node)
}

func (e *Env) GetNode(node NodeId) NodeCfg {
	idx := slices.IndexFunc(e.Routers, func(cfg RouterCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		idx = slices.IndexFunc(e.Clients, func(cfg ClientCfg) bool {
			return cfg.Id == node
		})
		if idx == -1 {
			panic("node " + string(node) + " not found")
		}
		return e.Clients[idx].NodeCfg
	}
	return e.Routers[idx].NodeCfg
}

func (e *Env) GetRouter(node NodeId) RouterCfg {
	idx := slices.IndexFunc(e.Routers, func(cfg RouterCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		panic("router " + string(node) + " not found")
	}

	return e.Routers[idx]
}

func (e *Env) GetClient(node NodeId) ClientCfg {
	idx := slices.IndexFunc(e.Clients, func(cfg ClientCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		panic("client " + string(node) + " not found")
	}

	return e.Clients[idx]
}
