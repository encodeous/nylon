package state

import (
	"slices"
	"time"
)

type Node string

type Neighbour struct {
	Id     Node
	Routes map[Node]PubRoute
	Eps    []*DynamicEndpoint
}

type Source struct {
	Id    Node
	Seqno uint16 // Sequence Number
	Sig   []byte // signature
}

type PubRoute struct {
	Src           Source
	PubMetric     uint16
	Retracted     bool
	LastPublished time.Time
}

type Route struct {
	PubRoute
	Fd uint16 // feasibility distance
	Nh Node   // next hop node
}

func (r *Route) Metric() uint16 {
	panic("TODO, select best dynamic endpoint and PubMetric")
}

func (s *State) GetNeighbour(node Node) *Neighbour {
	nIdx := slices.IndexFunc(s.Neighbours, func(neighbour *Neighbour) bool {
		return neighbour.Id == node
	})
	if nIdx == -1 {
		return nil
	}
	return s.Neighbours[nIdx]
}
