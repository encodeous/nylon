package state

import (
	"fmt"
	"slices"
)

// NodeIdBin is a compact binary identifier for a node, derived from the
// central config. It is used in dataplane packet headers in place of the
// variable-length NodeId string so the parser can run with a fixed-size
// header on the hot path.
//
// Value 0 is reserved as "invalid / unassigned". Valid binary ids begin at 1
// and are assigned deterministically (alphabetical order over the union of
// routers and clients in the central config). Both ends of a tunnel must hold
// the same central config to agree on the mapping; this is already a
// prerequisite for routing in nylon.
type NodeIdBin uint16

const InvalidNodeIdBin NodeIdBin = 0

// MaxNodeIdBin is the largest representable binary node id. The header
// reserves two bytes per id (big-endian uint16), so the upper bound is the
// 16-bit unsigned max.
const MaxNodeIdBin NodeIdBin = 0xFFFF

// NodeIdMap is an immutable bidirectional NodeId <-> NodeIdBin lookup table.
// Construct via BuildNodeIdMap and treat the returned value as read-only.
type NodeIdMap struct {
	byBin    []NodeId          // index 0 reserved, valid entries at 1..len-1
	byString map[NodeId]NodeIdBin
}

// BuildNodeIdMap returns a deterministic NodeId <-> NodeIdBin mapping derived
// from the given central config. The same input config produces the same
// mapping on every node, so peers always agree on the encoding.
//
// Returns an error if the config contains more than MaxNodeIdBin nodes.
func BuildNodeIdMap(c *CentralCfg) (*NodeIdMap, error) {
	ids := make([]NodeId, 0, len(c.Routers)+len(c.Clients))
	seen := make(map[NodeId]struct{}, len(c.Routers)+len(c.Clients))
	for _, r := range c.Routers {
		if _, ok := seen[r.Id]; ok {
			continue
		}
		seen[r.Id] = struct{}{}
		ids = append(ids, r.Id)
	}
	for _, cl := range c.Clients {
		if _, ok := seen[cl.Id]; ok {
			continue
		}
		seen[cl.Id] = struct{}{}
		ids = append(ids, cl.Id)
	}
	slices.Sort(ids)

	if uint64(len(ids)) > uint64(MaxNodeIdBin) {
		return nil, fmt.Errorf("network has %d nodes, exceeds NodeIdBin capacity of %d", len(ids), MaxNodeIdBin)
	}

	m := &NodeIdMap{
		byBin:    make([]NodeId, len(ids)+1), // slot 0 reserved
		byString: make(map[NodeId]NodeIdBin, len(ids)),
	}
	for i, id := range ids {
		bin := NodeIdBin(i + 1)
		m.byBin[bin] = id
		m.byString[id] = bin
	}
	return m, nil
}

// ToBin returns the binary id for a NodeId, or InvalidNodeIdBin if the node
// is not present in the mapping.
func (m *NodeIdMap) ToBin(id NodeId) (NodeIdBin, bool) {
	if m == nil {
		return InvalidNodeIdBin, false
	}
	b, ok := m.byString[id]
	return b, ok
}

// ToString returns the NodeId for a binary id, or ("", false) if the binary
// id is unassigned.
func (m *NodeIdMap) ToString(b NodeIdBin) (NodeId, bool) {
	if m == nil || b == InvalidNodeIdBin || int(b) >= len(m.byBin) {
		return "", false
	}
	id := m.byBin[b]
	if id == "" {
		return "", false
	}
	return id, true
}

// Len returns the number of nodes in the mapping.
func (m *NodeIdMap) Len() int {
	if m == nil {
		return 0
	}
	return len(m.byBin) - 1
}
