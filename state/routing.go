package state

import (
	"github.com/encodeous/nylon/protocol"
	"slices"
	"time"
)

type NodeId string

type IOPending struct {
	SeqnoReq map[Source]struct{}
	Updates  map[NodeId]*protocol.Ny_Update
}

type Neighbour struct {
	Id     NodeId
	Routes map[NodeId]PubRoute
	Eps    []*DynamicEndpoint
	IO     IOPending
}

type Source struct {
	Id    NodeId
	Seqno uint16 // Sequence Number
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
	Nh NodeId // next hop node
}

func (s *State) GetNeighbour(node NodeId) *Neighbour {
	nIdx := slices.IndexFunc(s.Neighbours, func(neighbour *Neighbour) bool {
		return neighbour.Id == node
	})
	if nIdx == -1 {
		return nil
	}
	return s.Neighbours[nIdx]
}
