package state

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

type NodeId string

// Source is a pair of a router-id and a prefix (Babel Section 2.7).
type Source struct {
	NodeId
	netip.Prefix
}

func (s Source) String() string {
	return fmt.Sprintf("(router: %s, prefix: %s)", s.NodeId, s.Prefix)
}

type Advertisement struct {
	NodeId
	Expiry        time.Time
	IsPassiveHold bool
	MetricFn      func() uint32
	ExpiryFn      func()
}
type RouterState struct {
	Id         NodeId
	SelfSeqno  map[netip.Prefix]uint16
	Routes     map[netip.Prefix]SelRoute
	Sources    map[Source]FD
	Neighbours []*Neighbour
	// Advertised is a map tracking the prefix and the time it will be advertised until
	Advertised map[netip.Prefix]Advertisement
}

func (s *RouterState) GetSeqno(prefix netip.Prefix) uint16 {
	seq, ok := s.SelfSeqno[prefix]
	if !ok {
		return 0
	}
	return seq
}

func (s *RouterState) SetSeqno(prefix netip.Prefix, seqno uint16) {
	s.SelfSeqno[prefix] = seqno
}

func (s *RouterState) StringRoutes() string {
	buf := make([]string, 0)
	for prefix, route := range s.Routes {
		buf = append(buf, fmt.Sprintf("%s via %s", prefix, route))
	}
	slices.Sort(buf)
	return strings.Join(buf, "\n")
}

type Neighbour struct {
	Id     NodeId
	Routes map[Source]NeighRoute
	Eps    []Endpoint
}

type FD struct {
	Seqno  uint16
	Metric uint32
}

type PubRoute struct {
	Source
	// FD will depend on which table the route is in. In the neighbour table,
	// it represents the metric advertised by the neighbour.
	// In the selected route table, it represents the metric that
	// the route will be advertised with.
	FD
}

func (r PubRoute) String() string {
	return fmt.Sprintf("(router: %s, prefix: %s, seqno: %d, metric: %d)", r.NodeId, r.Prefix, r.Seqno, r.Metric)
}

type NeighRoute struct {
	PubRoute
	ExpireAt time.Time // when the route expires
}

type SelRoute struct {
	PubRoute
	Nh          NodeId    // next hop node
	ExpireAt    time.Time // when the route expires
	RetractedBy []NodeId
}

func (r SelRoute) String() string {
	return fmt.Sprintf("(nh: %s, router: %s, prefix: %s, seqno: %d, metric: %d)", r.Nh, r.NodeId, r.Prefix, r.Seqno, r.Metric)
}

func (s *RouterState) GetNeighbour(node NodeId) *Neighbour {
	nIdx := slices.IndexFunc(s.Neighbours, func(neighbour *Neighbour) bool {
		return neighbour.Id == node
	})
	if nIdx == -1 {
		return nil
	}
	return s.Neighbours[nIdx]
}
