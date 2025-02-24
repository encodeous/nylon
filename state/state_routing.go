package state

import (
	"time"
)

type Node string

type Neighbour struct {
	Id     Node
	Routes map[Node]PubRoute
	Eps    []*DynamicEndpoint
}

func (n *Neighbour) BestEndpoint() *DynamicEndpoint {
	var best *DynamicEndpoint

	for _, link := range n.Eps {
		if best == nil || link.Metric() < best.Metric() || (link.IsActive() && !best.IsActive()) {
			best = link
		}
	}
	return best
}

type Route struct {
	PubRoute
	Fd uint16 // feasibility distance
	Nh Node   // next hop node
}

func (r *Route) Metric() uint16 {
	panic("TODO, select best dynamic endpoint and PubMetric")
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
