package state

import (
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strings"
)

// PubNodeCfg represents a central representation of a node
type PubNodeCfg struct {
	Id        Node
	Prefixes  []netip.Prefix
	PubKey    NyPublicKey
	Endpoints []netip.AddrPort
}

type CentralCfg struct {
	// the public key of the root CA
	RootPubKey NyPublicKey
	Nodes      []PubNodeCfg
	Graph      []string
	Version    uint64
}

// TODO: Allow node to be configured to NOT be a router
// NodeCfg represents local node-level configuration
type NodeCfg struct {
	// Node Private Key
	Key NyPrivateKey
	Id  Node
	// Address that the data plane can be accessed by
	Port uint16
}

func (n NodeCfg) GeneratePubCfg(extIp netip.Addr, port uint16, nylonIp netip.Prefix) PubNodeCfg {
	extDp := netip.AddrPortFrom(extIp, port)
	cfg := PubNodeCfg{
		Id: n.Id,
		Endpoints: []netip.AddrPort{
			extDp,
		},
		Prefixes: []netip.Prefix{
			nylonIp,
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
func ParseGraph(graph []string, nodes []string) ([]Pair[Node, Node], error) {
	// why can't we just have unordered_set<Pair<Node, Node>> :(

	pairings := make([]Pair[Node, Node], 0)

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
			interconnectNodes := make([]Node, 0)
			for _, name := range names {
				if slices.Contains(nodes, name) {
					for _, node := range interconnectNodes {
						if node != Node(name) {
							pairings = append(pairings, makeSortedPair(node, Node(name)))
						}
					}
					interconnectNodes = append(interconnectNodes, Node(name))
				} else {
					for _, node := range interconnectNodes {
						for _, grpNode := range groups[name] {
							if node != Node(grpNode) {
								pairings = append(pairings, makeSortedPair(node, Node(grpNode)))
							}
						}
					}
					for _, grpNode := range groups[name] {
						interconnectNodes = append(interconnectNodes, Node(grpNode))
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

func makeSortedPair(a Node, b Node) Pair[Node, Node] {
	if strings.Compare(string(a), string(b)) < 0 {
		return Pair[Node, Node]{a, b}
	} else {
		return Pair[Node, Node]{b, a}
	}
}

func (e Env) GetNodeBy(pkey NyPublicKey) *PubNodeCfg {
	for _, n := range e.Nodes {
		if n.PubKey == pkey {
			return &n
		}
	}
	return nil
}

func (e Env) GetPeers() []Node {
	allNodes := make([]string, 0)
	for _, node := range e.Nodes {
		allNodes = append(allNodes, string(node.Id))
	}
	graph, err := ParseGraph(e.Graph, allNodes)
	if err != nil {
		panic(err)
	}
	nodes := make([]Node, 0)
	for _, edge := range graph {
		var neighNode Node
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

func (e Env) GetPubNodeCfg(node Node) (PubNodeCfg, error) {
	idx := slices.IndexFunc(e.Nodes, func(cfg PubNodeCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		return PubNodeCfg{}, errors.New("node " + string(node) + " not found")
	}

	return e.Nodes[idx], nil
}

func (e Env) MustGetNode(node Node) PubNodeCfg {
	idx := slices.IndexFunc(e.Nodes, func(cfg PubNodeCfg) bool {
		return cfg.Id == node
	})
	if idx == -1 {
		panic("node " + string(node) + " not found")
	}

	return e.Nodes[idx]
}
