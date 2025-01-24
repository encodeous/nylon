package impl

import (
	"errors"
	"github.com/encodeous/nylon/mock"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"google.golang.org/protobuf/proto"
	"slices"
	"time"
)

type Router struct {
	// list of active neighbours
	Neighbours []*state.Neighbour
	Routes     map[state.Node]*state.Route
	SeqnoDedup *ttlcache.Cache[state.Node, state.Source]
	Self       *state.Source
}

func (r *Router) Cleanup(s *state.State) error {
	r.SeqnoDedup.Stop()
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
	r.Routes = make(map[state.Node]*state.Route)
	r.SeqnoDedup = ttlcache.New[state.Node, state.Source](ttlcache.WithTTL[state.Node, state.Source](30 * time.Second))
	go r.SeqnoDedup.Start()
	return nil
}

func IncrementSeqno(s *state.State) {
	r := Get[*Router](s)
	r.Self.Seqno++
	// TODO: Signature
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
		SeqnoPush: false,
		Updates:   make([]*protocol.CtlRouteUpdate_Params, 0),
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

func pushSeqnoUpdate(s *state.State, sources []state.Node) error {
	if len(sources) == 0 {
		return nil
	}
	r := Get[*Router](s)

	// broadcast routes
	pkt := protocol.CtlRouteUpdate{
		SeqnoPush: true,
		Updates:   make([]*protocol.CtlRouteUpdate_Params, 0),
	}

	for _, source := range sources {
		if source == r.Self.Id {
			// make self update
			pkt.Updates = append(pkt.Updates, &protocol.CtlRouteUpdate_Params{
				Source: mapToPktSource(r.Self),
				Metric: 0,
			})
		} else {
			route, ok := r.Routes[source]
			if ok {
				pkt.Updates = append(pkt.Updates, &protocol.CtlRouteUpdate_Params{
					Source: mapToPktSource(&route.Src),
					Metric: uint32(route.Metric),
				})
			}
		}
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
	retractions := protocol.CtlRouteUpdate{
		SeqnoPush: false,
		Updates:   make([]*protocol.CtlRouteUpdate_Params, 0),
	}

	improvedSeqno := make([]state.Node, 0)

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
						if SeqnoLt(tRoute.Src.Seqno, neighRoute.Src.Seqno) {
							improvedSeqno = append(improvedSeqno, neighRoute.Src.Id)
						}
						tRoute.Src = neighRoute.Src
						tRoute.Fd = metric
						tRoute.Link = link
						tRoute.Nh = neigh.Id
						tRoute.Retracted = false
					} else {
						// not feasible :(
						nh := tRoute.Nh
						if nh == neigh.Id {
							retract := false
							// route is currently selected
							if metric > tRoute.Fd {
								// retract our route!
								if !tRoute.Retracted {
									retract = true
								}
								tRoute.Metric = INF
								tRoute.Retracted = true
							} else {
								// update metric unconditionally, as it is <= Fd
								tRoute.Metric = metric
								tRoute.Fd = metric
								if metric == INF && !tRoute.Retracted {
									retract = true
								}
								tRoute.Retracted = retract
							}
							if retract {
								retractions.Updates = append(retractions.Updates, &protocol.CtlRouteUpdate_Params{
									Source: mapToPktSource(&tRoute.Src),
									Metric: INF,
								})
							}
						}
					}
					r.Routes[src] = tRoute
				} else if metric != INF {
					// add new route, if it is not retracted
					r.Routes[src] = &state.Route{
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
	improvedSeqno = slices.Compact(improvedSeqno)
	if len(improvedSeqno) > 0 {
		err := pushSeqnoUpdate(s, improvedSeqno)
		if err != nil {
			return err
		}
	}

	if len(retractions.Updates) > 0 {
		broadcast(s, &protocol.CtlMsg{Type: &protocol.CtlMsg_Route{Route: &retractions}})
	}

	// check for starvation
	for node, route := range r.Routes {
		if route.Metric == INF {
			// we dont have a valid route to this node

			prev := r.SeqnoDedup.Get(node)
			if prev != nil && SeqnoGe(prev.Value().Seqno, route.Src.Seqno) {
				continue // we have already sent such a request before
			}
			r.SeqnoDedup.Set(node, route.Src, ttlcache.DefaultTTL)

			broadcast(s, &protocol.CtlMsg{
				Type: &protocol.CtlMsg_SeqnoRequest{
					SeqnoRequest: mapToPktSource(&route.Src),
				},
			})
		}
	}

	return nil
}

// packet handlers
func routerHandleRouteUpdate(s *state.State, node state.Node, pkt *protocol.CtlRouteUpdate) error {
	r := Get[*Router](s)

	nidx := slices.IndexFunc(r.Neighbours, func(neighbour *state.Neighbour) bool {
		return neighbour.Id == node
	})
	neigh := r.Neighbours[nidx]
	neigh.Routes = make(map[state.Node]state.PubRoute)
	hasRetractions := false
	for _, update := range pkt.Updates {
		cur, ok := neigh.Routes[state.Node(update.Source.Id)]
		if ok {
			hasRetractions = hasRetractions || !cur.Retracted && update.Metric == INF
		}
		neigh.Routes[state.Node(update.Source.Id)] = state.PubRoute{
			Src:       mapFromPktSource(update.Source),
			Metric:    uint16(update.Metric),
			Retracted: update.Metric == INF,
		}
	}
	if hasRetractions || pkt.SeqnoPush {
		return updateRoutes(s)
	}
	return nil
}

func routerHandleSeqnoRequest(s *state.State, node state.Node, pkt *protocol.Source) error {
	r := Get[*Router](s)
	src := mapFromPktSource(pkt)

	// TODO: Verify src sig

	var fSrc *state.Source

	froute, ok := r.Routes[src.Id]

	if ok && SeqnoGt(froute.Src.Seqno, src.Seqno) {
		fSrc = &froute.Src
	} else if s.Id == src.Id {
		if r.Self.Seqno <= src.Seqno {
			r.Self.Seqno = src.Seqno
			IncrementSeqno(s)
		}
		fSrc = r.Self
	}

	if fSrc != nil {
		// we have a better one cached, no need to forward
		return pushSeqnoUpdate(s, []state.Node{fSrc.Id})
	} else {
		prev := r.SeqnoDedup.Get(src.Id)
		if prev != nil && SeqnoGe(prev.Value().Seqno, src.Seqno) {
			return nil // we have already sent such a request before
		}
		r.SeqnoDedup.Set(src.Id, src, ttlcache.DefaultTTL)
		broadcast(s, &protocol.CtlMsg{Type: &protocol.CtlMsg_SeqnoRequest{SeqnoRequest: pkt}})
	}
	return nil
}
