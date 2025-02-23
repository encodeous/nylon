package state

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"github.com/encodeous/polyamide/conn"
	"github.com/jellydator/ttlcache/v3"
	"go.step.sm/crypto/x25519"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"
)

type NyModule interface {
	Init(s *State) error
	Cleanup(s *State) error
}

type State struct {
	*Env
	TrustedNodes map[Node]ed25519.PublicKey
	Modules      map[string]NyModule
}

type Env struct {
	DispatchChannel chan<- func(s *State) error
	LinkChannel     chan<- CtlLink
	CentralCfg
	NodeCfg
	Context context.Context
	Cancel  context.CancelCauseFunc
	PingBuf *ttlcache.Cache[uint64, LinkPing]
	Log     *slog.Logger
}

type DpEndpoint struct {
	Name       string
	RemoteInit bool          `yaml:"-"`
	WgEndpoint conn.Endpoint `yaml:"-"`
	Ep         netip.AddrPort
}

func (ep *DpEndpoint) GetWgEndpoint() conn.Endpoint {
	if ep.WgEndpoint == nil || ep.WgEndpoint.DstToString() != ep.Ep.String() {
		ep.WgEndpoint = &conn.StdNetEndpoint{AddrPort: ep.Ep}
	}
	return ep.WgEndpoint
}

type NyPrivateKey []byte
type NyPublicKey []byte

// TODO: Allow node to be configured to NOT be a router
// NodeCfg represents local node-level configuration
type NodeCfg struct {
	// Node Private Key
	Key NyPrivateKey
	Id  Node
	// Address and port that the control plane listens on
	CtlBind netip.AddrPort
	// Address that the data plane can be accessed by
	DpPort uint16
}

func GenerateKey() NyPrivateKey {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		panic(err)
	}
	return key[:]
}

func (k NyPrivateKey) XPubkey() NyPublicKey {
	val, err := x25519.PrivateKey(k).PublicKey()
	if err != nil {
		panic(err)
	}
	return NyPublicKey(val)
}

func (k NyPrivateKey) EdPubkey() []byte {
	val, err := x25519.PrivateKey(k).PublicKey()
	if err != nil {
		panic(err)
	}
	val2, err := val.ToEd25519()
	if err != nil {
		panic(err)
	}
	return val2
}

func (k NyPublicKey) EdPubkey() []byte {
	val, err := (x25519.PublicKey(k)).ToEd25519()
	if err != nil {
		panic(err)
	}
	return val
}

func (n NodeCfg) GeneratePubCfg(extIp netip.Addr, nylonIp netip.Addr) PubNodeCfg {
	extDp := netip.AddrPortFrom(extIp, n.DpPort)
	cfg := PubNodeCfg{
		Id:      n.Id,
		CtlAddr: []string{RepAddr(n.CtlBind, extIp).String()},
		DpAddr: []*DpEndpoint{
			{fmt.Sprintf("%s-pub", n.Id), false, nil, extDp},
		},
		NylonAddr: nylonIp,
	}
	if n.Key != nil {
		cfg.PubKey = n.Key.XPubkey()
	}
	return cfg
}

// PubNodeCfg represents a central representation of a node
type PubNodeCfg struct {
	Id        Node
	NylonAddr netip.Addr
	PubKey    NyPublicKey
	CtlAddr   []string
	DpAddr    []*DpEndpoint
}

type CentralCfg struct {
	// the public key of the root CA
	RootPubKey  NyPublicKey
	Nodes       []PubNodeCfg
	Graph       []string
	mockWeights []Triple[Node, Node, *time.Duration]
	Version     uint64
}

func parseSymbolList(s string, validSymbols []string) ([]string, error) {
	spl := strings.Split(strings.TrimSpace(s), ",")
	line := make([]string, len(spl))
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
	return slices.Compact(line), nil
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
						pairings = append(pairings, makeSortedPair(node, Node(name)))
					}
					interconnectNodes = append(interconnectNodes, Node(name))
				} else {
					for _, node := range interconnectNodes {
						for _, grpNode := range groups[name] {
							pairings = append(pairings, makeSortedPair(node, Node(grpNode)))
						}
					}
					for _, grpNode := range groups[name] {
						interconnectNodes = append(interconnectNodes, Node(grpNode))
					}
				}
			}
			sort.Slice(pairings, func(i, j int) bool {
				x := strings.Compare(string(pairings[i].V1), string(pairings[i].V1))
				y := strings.Compare(string(pairings[i].V2), string(pairings[i].V2))
				return x < 0 || x == 0 && y < 0
			})
			slices.Compact(pairings)
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

// Dispatch Dispatches the function to run on the main thread without waiting for it to complete
func (e Env) Dispatch(fun func(*State) error) {
	defer func() {
		if r := recover(); r != nil {
			e.Cancel(fmt.Errorf("panic: %v", r))
		}
	}()
	e.DispatchChannel <- fun
}

// DispatchWait Dispatches the function to run on the main thread and wait for it to complete
func (e Env) DispatchWait(fun func(*State) (any, error)) (any, error) {
	ret := make(chan Pair[any, error])
	e.DispatchChannel <- func(s *State) error {
		res, err := fun(s)
		ret <- Pair[any, error]{res, err}
		return err
	}
	select {
	case res := <-ret:
		return res.V1, res.V2
	case <-e.Context.Done():
		return nil, e.Context.Err()
	}
}

func (e Env) scheduledTask(fun func(*State) error, delay time.Duration) {
	time.Sleep(delay)
	e.Dispatch(fun)
}

func (e Env) ScheduleTask(fun func(*State) error, delay time.Duration) {
	go e.scheduledTask(fun, delay)
}

func (e Env) repeatedTask(fun func(*State) error, delay time.Duration) {
	for e.Context.Err() == nil {
		e.Dispatch(fun)
		time.Sleep(delay)
	}
}

func (e Env) RepeatTask(fun func(*State) error, delay time.Duration) {
	go e.repeatedTask(fun, delay)
}
