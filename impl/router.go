package impl

import (
	"errors"
	"github.com/encodeous/nylon/mock"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"slices"
)

type Router struct {
	// list of active neighbours
	Neighbours []*state.Neighbour
	Routes     map[state.Node]state.Route
	Self       *state.Source
}

func (r *Router) Cleanup(s *state.State) error {
	// do nothing
	return nil
}

func (r *Router) Init(s *state.State) error {
	s.Log.Debug("init router")
	s.Env.RepeatTask(fullRouteUpdate, RouteUpdateDelay)
	r.Self = &state.Source{
		Id:    s.Id,
		Seqno: 0,
		Sig:   nil,
	}
	r.Routes = make(map[state.Node]state.Route)
	return nil
}

func RemoveLink(s *state.State, cfg state.PubNodeCfg, removeLink state.CtlLink) {
	r := Get[*Router](s)
	nidx := slices.IndexFunc(r.Neighbours, func(neighbour *state.Neighbour) bool {
		return neighbour.Id == cfg.Id
	})
	if nidx != -1 {
		neigh := r.Neighbours[nidx]
		lidx := slices.IndexFunc(neigh.CtlLinks, func(link state.CtlLink) bool {
			return link.Id() == removeLink.Id()
		})
		if lidx != -1 {
			r.Neighbours[nidx].CtlLinks = append(r.Neighbours[nidx].CtlLinks[:lidx], r.Neighbours[nidx].CtlLinks[lidx+1:]...)
		}
	}
}

func AddNeighbour(s *state.State, cfg state.PubNodeCfg, link state.CtlLink) error {
	r := Get[*Router](s)
	idx := slices.IndexFunc(r.Neighbours, func(neighbour *state.Neighbour) bool {
		return neighbour.Id == cfg.Id
	})
	if idx == -1 {
		s.Log.Debug("discovered neighbour", "node", cfg.Id)

		var dplinks []state.DpLink
		for _, w := range mock.GetMockWeight(s.Id, cfg.Id, s.CentralCfg) {
			dplinks = append(dplinks, mock.MockLink{
				VId:     uuid.UUID{},
				VMetric: w,
			})
		}

		r.Neighbours = append(r.Neighbours, &state.Neighbour{
			Id:       cfg.Id,
			Routes:   make(map[state.Node]state.PubRoute),
			DpLinks:  dplinks,
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

func broadcast(s *state.State, message proto.Message) {
	r := Get[*Router](s).Neighbours
	go func() {
		for _, neighbour := range r {
			if len(neighbour.CtlLinks) != 0 {
				err := neighbour.CtlLinks[0].WriteMsg(message)
				if err != nil {
					s.Env.Log.Error("error while broadcasting", "err", err.Error())
				}
			}
		}
	}()
}

func mapToPktSource(source *state.Source) *protocol.Source {
	return &protocol.Source{
		Id:    string(source.Id),
		Seqno: uint32(source.Seqno),
		Sig:   source.Sig,
	}
}
func mapFromPktSource(source *protocol.Source) state.Source {
	return state.Source{
		Id:    state.Node(source.Id),
		Seqno: uint16(source.Seqno),
		Sig:   source.Sig,
	}
}

func fullRouteUpdate(s *state.State) error {
	r := Get[*Router](s)
	err := updateRoutes(s)
	if err != nil {
		return err
	}

	// broadcast routes

	pkt := protocol.CtlRouteUpdate{
		Urgent:  false,
		Updates: make([]*protocol.CtlRouteUpdate_Params, 0),
	}

	// make self update
	pkt.Updates = append(pkt.Updates, &protocol.CtlRouteUpdate_Params{
		Source: mapToPktSource(r.Self),
		Metric: 0,
	})

	// write route table
	for _, route := range r.Routes {
		pkt.Updates = append(pkt.Updates, &protocol.CtlRouteUpdate_Params{
			Source: mapToPktSource(&route.Src),
			Metric: uint32(route.Metric),
		})
	}

	wrapped := protocol.CtlMsg{Type: &protocol.CtlMsg_Route{Route: &pkt}}
	broadcast(s, &wrapped)
	return nil
}

func dbgPrintRouteTable(s *state.State) {
	r := Get[*Router](s)
	if len(r.Routes) != 0 {
		s.Log.Debug("--- route table ---")
	}
	for _, route := range r.Routes {
		s.Log.Debug(string(route.Src.Id), "seqno", route.Src.Seqno, "metric", route.Metric, "fd", route.Fd, "ret", route.Retracted, "nh", route.Nh)
	}
}

func updateRoutes(s *state.State) error {
	r := Get[*Router](s)
	var newRetractions []state.Node

	dbgPrintRouteTable(s)

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
							Src:       neighRoute.Src,
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

// packet handlers

func routerHandleRouteUpdate(s *state.State, node state.Node, pkt *protocol.CtlRouteUpdate) error {
	r := Get[*Router](s)

	if pkt.Urgent {
		// TODO
		// only for retractions and seqno updates
	} else {
		nidx := slices.IndexFunc(r.Neighbours, func(neighbour *state.Neighbour) bool {
			return neighbour.Id == node
		})
		neigh := r.Neighbours[nidx]
		neigh.Routes = make(map[state.Node]state.PubRoute)
		for _, update := range pkt.Updates {
			if update.Metric == INF {
				s.Log.Warn("peer provided impossible metric", "metric", update.Metric, "node", node)
			}
			neigh.Routes[state.Node(update.Source.Id)] = state.PubRoute{
				Src:       mapFromPktSource(update.Source),
				Metric:    uint16(update.Metric),
				Retracted: false,
			}
		}
	}

	return nil
}
