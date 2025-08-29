package state

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type NodeId string

// ServiceId maps to a real network prefix
type ServiceId string

// Source is a pair of a router-id and a prefix (Babel Section 2.7). In this case, we use a service identifier
type Source struct {
	NodeId
	ServiceId
}

func (s Source) String() string {
	return fmt.Sprintf("(router: %s, svc: %s)", s.NodeId, s.ServiceId)
}

type Advertisement struct {
	NodeId
	Expiry        time.Time
	IsPassiveHold bool
}
type RouterState struct {
	Id         NodeId
	SelfSeqno  map[ServiceId]uint16
	Routes     map[ServiceId]SelRoute
	Sources    map[Source]FD
	Neighbours []*Neighbour
	// Advertised is a map tracking the service id and the time it will be advertised until
	Advertised map[ServiceId]Advertisement
	// DisableRouting indicates that this node should not route traffic for other nodes
	DisableRouting bool
}

func (s *RouterState) GetSeqno(id ServiceId) uint16 {
	seq, ok := s.SelfSeqno[id]
	if !ok {
		return 0
	}
	return seq
}

func (s *RouterState) SetSeqno(id ServiceId, seqno uint16) {
	s.SelfSeqno[id] = seqno
}

func (s *RouterState) StringRoutes() string {
	buf := make([]string, 0)
	for svc, route := range s.Routes {
		buf = append(buf, fmt.Sprintf("%s via %s", svc, route))
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
	Metric uint16
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
	return fmt.Sprintf("(router: %s, svc: %s, seqno: %d, metric: %d)", r.NodeId, r.ServiceId, r.Seqno, r.Metric)
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
	return fmt.Sprintf("(nh: %s, router: %s, svc: %s, seqno: %d, metric: %d)", r.Nh, r.NodeId, r.ServiceId, r.Seqno, r.Metric)
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
