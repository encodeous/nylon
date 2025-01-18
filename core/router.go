package core

import (
	"errors"
	"github.com/encodeous/nylon/core/network"
)

type Node string

type Neighbour struct {
	Id      Node
	Routes  map[Node]PubRoute
	NodeSrc map[Node]Source
	DpLinks []network.DpLink
	CtlLink network.CtlLink
	Metric  uint16
}

type Route struct {
	PubRoute
	Fd   uint16 // feasibility distance
	Link network.DpLink
	Nh   Node // next hop node
}

type Source struct {
	Id    Node
	Seqno uint16 // Sequence Number
	Sig   []byte // signature
}

type PubRoute struct {
	Src       Source
	Metric    uint16
	Retracted bool
}

type Router struct {
	Neighbours []*Neighbour
	Routes     map[Node]Route
	Self       Node
}

func (r *Router) Update() error {
	err := r.updateRoutes()
	if err != nil {
		return err
	}
	return nil
}

func (r *Router) updateRoutes() error {
	var newRetractions []Node

	for _, neigh := range r.Neighbours {
		for _, link := range neigh.DpLinks {
			if link.Metric() == 0 {
				return errors.New("metric cannot be zero")
			}
			for src, neighRoute := range neigh.Routes {
				if src == r.Self {
					continue
				}

				metric := AddSeqno(link.Metric(), neighRoute.Metric)

				tRoute, ok := r.Routes[src]

				if ok {
					// route exists
					if IsFeasible(tRoute, neighRoute, metric) {
						// feasible, update existing route
						tRoute.Metric = metric
						tRoute.Src = neighRoute.Src
						tRoute.Fd = metric
						tRoute.Link = link
						tRoute.Nh = neigh.Id
						tRoute.Retracted = false
					} else {
						// not feasible :(
						nh := tRoute.Nh
						if nh == neigh.Id {
							// route is currently preferred
							if metric > tRoute.Fd {
								// retract our route!
								if !tRoute.Retracted {
									newRetractions = append(newRetractions, tRoute.Src.Id)
								}
								tRoute.Metric = INF
								tRoute.Retracted = true
							} else {
								// update metric unconditionally, as it is <= Fd
								tRoute.Metric = metric
								tRoute.Fd = metric
								tRoute.Retracted = false
							}
						}
					}
					r.Routes[src] = tRoute
				} else if metric != INF {
					// add new route, if it is not retracted
					r.Routes[src] = Route{
						PubRoute: PubRoute{
							Src:       neigh.NodeSrc[src],
							Metric:    metric,
							Retracted: false,
						},
						Fd:   metric,
						Nh:   neigh.Id,
						Link: link,
					}
				}
			}
		}
	}

	return nil
}

// region utils

const (
	INF = 65535
)

func AddSeqno(a, b uint16) uint16 {
	if a == INF || b == INF {
		return INF
	} else {
		return min(INF-1, a+b)
	}
}

func SeqnoLt(a, b uint16) bool {
	x := (b - a) % 63336
	return 0 < x && x < 32768
}

func IsFeasible(curRoute Route, newRoute PubRoute, metric uint16) bool {
	if SeqnoLt(newRoute.Src.Seqno, curRoute.Src.Seqno) {
		return false
	}

	if metric < curRoute.Fd ||
		SeqnoLt(curRoute.Src.Seqno, newRoute.Src.Seqno) ||
		(metric == curRoute.Fd && curRoute.Metric == INF) {
		return true
	}
	return false
}

// endregion
