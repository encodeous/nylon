package impl

import (
	"errors"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
	"slices"
	"time"
)

type Router struct {
	// list of active neighbours
	Routes                map[state.NodeId]*state.Route
	SeqnoDedup            *ttlcache.Cache[state.NodeId, state.Source]
	Self                  *state.Source
	LastStarvationRequest time.Time
	Clients               []state.NodeId
}

func (r *Router) Cleanup(s *state.State) error {
	r.SeqnoDedup.Stop()
	return nil
}

func (r *Router) Init(s *state.State) error {
	s.Log.Debug("init router")
	s.Env.RepeatTask(func(s *state.State) error {
		return pushRouteTable(s, nil)
	}, state.RouteUpdateDelay)
	s.Env.RepeatTask(checkStarvation, state.StarvationDelay)
	r.Self = &state.Source{
		Id:    s.Id,
		Seqno: 0,
	}
	r.Routes = make(map[state.NodeId]*state.Route)
	r.SeqnoDedup = ttlcache.New[state.NodeId, state.Source](
		ttlcache.WithTTL[state.NodeId, state.Source](state.SeqnoDedupTTL),
		ttlcache.WithDisableTouchOnHit[state.NodeId, state.Source](),
	)
	go r.SeqnoDedup.Start()
	return nil
}

func pushRouteTable(s *state.State, to *state.NodeId) error {
	r := Get[*Router](s)
	err := updateRoutes(s)
	if err != nil {
		return err
	}

	dbgPrintRouteTable(s)

	// broadcast routes
	updates := make([]*protocol.Ny_Update, 0)

	// make self update
	updates = append(updates, &protocol.Ny_Update{
		Source: mapToPktSource(r.Self),
		Metric: 0,
	})

	// write route table
	if !s.DisableRouting {
		for _, route := range r.Routes {
			updates = append(updates, &protocol.Ny_Update{
				Source: mapToPktSource(&route.Src),
				Metric: uint32(route.PubMetric),
			})
		}
	}

	if to == nil {
		broadcastUpdates(s, updates, false, s.Neighbours)
	} else {
		broadcastUpdates(s, updates, false, []*state.Neighbour{
			s.GetNeighbour(*to),
		})
	}

	return nil
}

func pushSeqnoUpdate(s *state.State, sources []state.NodeId) error {
	if len(sources) == 0 {
		return nil
	}
	r := Get[*Router](s)

	// broadcast routes
	updates := make([]*protocol.Ny_Update, 0)

	for _, source := range sources {
		if source == r.Self.Id {
			// make self update
			updates = append(updates, &protocol.Ny_Update{
				Source: mapToPktSource(r.Self),
				Metric: 0,
			})
		} else if !s.DisableRouting {
			route, ok := r.Routes[source]
			if ok {
				updates = append(updates, &protocol.Ny_Update{
					Source: mapToPktSource(&route.Src),
					Metric: uint32(route.PubMetric),
				})
			}
		}
	}

	broadcastUpdates(s, updates, true, s.Neighbours)
	return nil
}

func updateRoutes(s *state.State) error {
	r := Get[*Router](s)
	retractions := make([]*protocol.Ny_Update, 0)
	improvedSeqno := make([]state.NodeId, 0)

	// basically bellman ford algorithm

	if state.DBG_log_router {
		s.Log.Debug("--- computing routing table ---")
	}

	for _, neigh := range s.Neighbours {
		if state.DBG_log_router {
			s.Log.Debug(" -- neighbour --", "id", neigh.Id)
		}
		bestEp := neigh.BestEndpoint()

		if bestEp != nil && bestEp.Metric() == 0 {
			s.Log.Warn(" link metric is zero")
			return errors.New(" metric cannot be zero")
		}

		if state.DBG_log_router {
			if bestEp != nil {
				s.Log.Debug(" selected", "met", bestEp.Metric())
			} else {
				s.Log.Debug(" no link to neighbour")
			}
		}

		for src, neighRoute := range neigh.Routes {
			if src == s.Id {
				continue
			}

			metric := state.INF

			if bestEp != nil {
				metric = AddMetric(bestEp.Metric(), neighRoute.PubMetric)
				metric = AddMetric(metric, state.HopCost)
			}

			if state.DBG_log_router {
				s.Log.Debug("  - eval neigh route -", "src", src, "met", metric, "nh", neigh.Id)
			}

			tRoute, ok := r.Routes[src]

			if ok {
				if SeqnoLt(neighRoute.Src.Seqno, tRoute.Src.Seqno) {
					if state.DBG_log_router {
						s.Log.Debug("  dropped, new seqno < old seqno")
						continue
					}
				}
				if state.DBG_log_router {
					s.Log.Debug("  existing route", "src", src, "met", metric, "nh", tRoute.Nh)
				}
				// route exists
				if IsFeasible(tRoute, neighRoute, metric) && bestEp != nil {
					if state.DBG_log_router {
						s.Log.Debug("  feasible, selected")
					}
					// feasible, update existing route, if matching switch heuristic
					if tRoute.Nh != neigh.Id && !SwitchHeuristic(tRoute, neighRoute, metric, bestEp.MetricRange()) && !tRoute.Retracted {
						// dont update this route, as it might cause oscillations
						continue
					}
					if tRoute.Nh != neigh.Id {
						dbgPrintRouteChanges(s, tRoute, &neighRoute, neigh.Id, metric)
					}
					tRoute.PubMetric = metric
					if SeqnoLt(tRoute.Src.Seqno, neighRoute.Src.Seqno) {
						improvedSeqno = append(improvedSeqno, neighRoute.Src.Id)
					}
					tRoute.Src = neighRoute.Src
					tRoute.Fd = metric
					tRoute.Nh = neigh.Id
					tRoute.Retracted = false
				} else {
					// not feasible :(
					nh := tRoute.Nh
					if nh == neigh.Id {
						retract := false
						// route is currently selected
						if metric > tRoute.Fd {
							if state.DBG_log_router {
								s.Log.Debug("  not feasible, retract (new-met > fd)")
							}
							// retract our route!
							if !tRoute.Retracted {
								retract = true
							}
							tRoute.PubMetric = state.INF
							tRoute.Retracted = true
						} else {
							if state.DBG_log_router {
								s.Log.Debug("  not feasible, but (new-met <= fd)")
							}
							// update metric unconditionally, as it is <= Fd
							tRoute.PubMetric = metric
							tRoute.Fd = metric
							if metric == state.INF && !tRoute.Retracted {
								retract = true
							}
							tRoute.Retracted = retract
						}
						if retract {
							metric = state.INF
							dbgPrintRouteChanges(s, tRoute, &neighRoute, neigh.Id, metric)
							retractions = append(retractions, &protocol.Ny_Update{
								Source: mapToPktSource(&tRoute.Src),
								Metric: uint32(state.INF),
							})
						}
					}
				}
				r.Routes[src] = tRoute
			} else if metric != state.INF {
				if state.DBG_log_router {
					s.Log.Debug("  new route! added to table")
				}
				dbgPrintRouteChanges(s, tRoute, &neighRoute, neigh.Id, metric)
				// add new route, if it is not retracted
				r.Routes[src] = &state.Route{
					PubRoute: state.PubRoute{
						Src:       neighRoute.Src,
						PubMetric: metric,
						Retracted: false,
					},
					Fd: metric,
					Nh: neigh.Id,
				}
			}
		}
	}

	// retract routes that the neighbour no longer publishes
	for _, neigh := range s.Neighbours {
		for src, route := range r.Routes {
			if route.Nh == neigh.Id && !route.Retracted {
				if _, ok := neigh.Routes[src]; !ok {
					route.Retracted = true
					route.PubMetric = state.INF
					retractions = append(retractions, &protocol.Ny_Update{
						Source: mapToPktSource(&route.Src),
						Metric: uint32(state.INF),
					})
					dbgPrintRouteChanges(s, route, nil, neigh.Id, state.INF)
				}
			}
		}
	}

	// retract published client routes
	for src, route := range r.Routes {
		if route.Nh == s.Id && !slices.Contains(r.Clients, src) && !route.Retracted {
			route.Retracted = true
			route.PubMetric = state.INF
			retractions = append(retractions, &protocol.Ny_Update{
				Source: mapToPktSource(&route.Src),
				Metric: uint32(state.INF),
			})
			dbgPrintRouteChanges(s, route, nil, s.Id, state.INF)
		}
	}

	slices.Sort(improvedSeqno)
	improvedSeqno = slices.Compact(improvedSeqno)
	if len(improvedSeqno) > 0 {
		err := pushSeqnoUpdate(s, improvedSeqno)
		if err != nil {
			return err
		}
	}

	if len(retractions) > 0 {
		broadcastUpdates(s, retractions, true, s.Neighbours)
	}

	return checkStarvation(s)
}

func checkStarvation(s *state.State) error {
	r := Get[*Router](s)
	starved := false
	// check for starvation
	if time.Now().Sub(r.LastStarvationRequest) > state.StarvationDelay {
		for node, route := range r.Routes {
			bestMetric := state.INF
			if route.Nh == s.Id {
				// for clients directly connected to the current node
				bestMetric = route.PubMetric
			} else {
				neigh := s.GetNeighbour(route.Nh)
				bestEp := neigh.BestEndpoint()
				if bestEp != nil {
					bestMetric = bestEp.Metric()
				}
			}

			if bestMetric == state.INF || route.PubMetric == state.INF {
				// we dont have a valid route to this node
				starved = true

				prev := r.SeqnoDedup.Get(node)
				if prev != nil && SeqnoGe(prev.Value().Seqno, route.Src.Seqno) {
					continue // we have already sent such a request before
				}
				r.SeqnoDedup.Set(node, route.Src, ttlcache.DefaultTTL)

				broadcastSeqnoRequest(s, mapToPktSource(&route.Src), s.Neighbours)
			}
		}
	}
	if starved {
		r.LastStarvationRequest = time.Now()
	}
	return nil
}

func (r *Router) updatePassiveClient(client state.NodeId) {
	// inserts an artificial route into the table
	if _, ok := r.Routes[client]; !ok {
		r.Routes[client] = &state.Route{
			PubRoute: state.PubRoute{
				Src: state.Source{
					Id:    client,
					Seqno: 0,
				},
			},
		}
	}
	// since the client can only connect to a single node, we know we have the best link to it
	k := r.Routes[client]
	k.PubMetric = 0
	k.Fd = 0
	k.Nh = r.Self.Id
	k.Retracted = false
	k.LastPublished = time.Now()
}

// packet handlers
func routerHandleRouteUpdate(s *state.State, node state.NodeId, pkt *protocol.Ny_UpdateBundle) error {
	neigh := s.GetNeighbour(node)
	hasRetractions := false
	for _, update := range pkt.Updates {
		cur, ok := neigh.Routes[state.NodeId(update.Source.Id)]
		if ok {
			hasRetractions = hasRetractions || !cur.Retracted && update.Metric == uint32(state.INF)
		}
		neigh.Routes[state.NodeId(update.Source.Id)] = state.PubRoute{
			Src:           mapFromPktSource(update.Source),
			PubMetric:     uint16(update.Metric),
			Retracted:     update.Metric == uint32(state.INF),
			LastPublished: time.Now(),
		}
	}
	if hasRetractions || pkt.SeqnoPush {
		return updateRoutes(s)
	}
	return nil
}

func routerHandleSeqnoRequest(s *state.State, neigh state.NodeId, pkt *protocol.Ny_Source) error {
	r := Get[*Router](s)
	src := mapFromPktSource(pkt)

	var fSrc *state.Source

	if s.Id == src.Id {
		// we are the node in question!
		if SeqnoLe(r.Self.Seqno, src.Seqno) {
			r.Self.Seqno = src.Seqno + 1
		}
		fSrc = r.Self
	} else if !s.DisableRouting {
		froute, ok := r.Routes[src.Id]

		if ok && SeqnoGt(froute.Src.Seqno, src.Seqno) {
			fSrc = &froute.Src
		} else if slices.Contains(r.Clients, src.Id) {
			// client is directly connected to us!
			clientSrc := &r.Routes[src.Id].Src
			if SeqnoLe(clientSrc.Seqno, src.Seqno) {
				clientSrc.Seqno = src.Seqno + 1
			}
			fSrc = clientSrc
		}
	}

	if fSrc != nil {
		// we have a better one cached, we can respond to this request
		return pushSeqnoUpdate(s, []state.NodeId{fSrc.Id})
	} else {
		prev := r.SeqnoDedup.Get(src.Id)
		if prev != nil && SeqnoGe(prev.Value().Seqno, src.Seqno) {
			return nil // we have already sent such a request before
		}
		r.SeqnoDedup.Set(src.Id, src, ttlcache.DefaultTTL)
		broadcastSeqnoRequest(s, pkt, s.Neighbours)
	}
	return nil
}
