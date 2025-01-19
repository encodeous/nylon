package impl

import (
	"errors"
	"github.com/encodeous/nylon/state"
	"slices"
)

type Router struct {
	// list of active neighbours
	Neighbours []*state.Neighbour
	Routes     map[state.Node]state.Route
}

func (r *Router) Init(s *state.State) error {
	s.Log.Debug("init router")
	return nil
}

func (r *Router) AddNeighbour(s *state.State, cfg state.PubNodeCfg, link state.CtlLink) error {
	idx := slices.IndexFunc(r.Neighbours, func(neighbour *state.Neighbour) bool {
		return neighbour.Id == cfg.Id
	})
	if idx == -1 {
		s.Log.Debug("discovered neighbour", "node", cfg.Id)
		r.Neighbours = append(r.Neighbours, &state.Neighbour{
			Id:       cfg.Id,
			Routes:   make(map[state.Node]state.PubRoute),
			NodeSrc:  nil,
			DpLinks:  nil,
			CtlLinks: []state.CtlLink{link},
			Metric:   1,
		})
	} else {
		s.Log.Debug("added new link to existing neighbour", "node", cfg.Id)
		neigh := r.Neighbours[idx]
		neigh.CtlLinks = append(neigh.CtlLinks, link)
	}
	return nil
}

func (r *Router) Update(s *state.State) error {
	err := r.updateRoutes(s)
	if err != nil {
		return err
	}
	return nil
}

func (r *Router) updateRoutes(s *state.State) error {
	var newRetractions []state.Node

	// basically bellman ford algorithm

	for _, neigh := range r.Neighbours {
		for _, link := range neigh.DpLinks {
			if link.Metric() == 0 {
				s.Log.Warn("link metric is zero")
				return errors.New("metric cannot be zero")
			}
			for src, neighRoute := range neigh.Routes {
				if src == s.Id {
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
					r.Routes[src] = state.Route{
						PubRoute: state.PubRoute{
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
