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
	Routes                map[state.Node]*state.Route
	SeqnoDedup            *ttlcache.Cache[state.Node, state.Source]
	Self                  *state.Source
	LastStarvationRequest time.Time
}

func (r *Router) Cleanup(s *state.State) error {
	r.SeqnoDedup.Stop()
	return nil
}

func (r *Router) Init(s *state.State) error {
	s.Log.Debug("init router")
	s.Env.RepeatTask(fullRouteUpdate, state.RouteUpdateDelay)
	s.Env.RepeatTask(checkStarvation, state.StarvationDelay)
	r.Self = &state.Source{
		Id:    s.Id,
		Seqno: 0,
		Sig:   nil,
	}
	r.Routes = make(map[state.Node]*state.Route)
	r.SeqnoDedup = ttlcache.New[state.Node, state.Source](
		ttlcache.WithTTL[state.Node, state.Source](state.SeqnoDedupTTL),
		ttlcache.WithDisableTouchOnHit[state.Node, state.Source](),
	)
	go r.SeqnoDedup.Start()
	return nil
}

func fullRouteUpdate(s *state.State) error {
	r := Get[*Router](s)
	err := updateRoutes(s)
	if err != nil {
		return err
	}

	// broadcast routes

	updates := make([]*protocol.Ny_Update, 0)

	// make self update
	updates = append(updates, &protocol.Ny_Update{
		Source: mapToPktSource(r.Self),
		Metric: 0,
	})

	// write route table
	for _, route := range r.Routes {
		updates = append(updates, &protocol.Ny_Update{
			Source: mapToPktSource(&route.Src),
			Metric: uint32(route.PubMetric),
		})
	}

	broadcastUpdates(s, updates, false)
	return nil
}

func pushSeqnoUpdate(s *state.State, sources []state.Node) error {
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
		} else {
			route, ok := r.Routes[source]
			if ok {
				updates = append(updates, &protocol.Ny_Update{
					Source: mapToPktSource(&route.Src),
					Metric: uint32(route.PubMetric),
				})
			}
		}
	}

	broadcastUpdates(s, updates, true)
	return nil
}

func updateRoutes(s *state.State) error {
	r := Get[*Router](s)
	retractions := make([]*protocol.Ny_Update, 0)

	improvedSeqno := make([]state.Node, 0)

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
	slices.Sort(improvedSeqno)
	improvedSeqno = slices.Compact(improvedSeqno)
	if len(improvedSeqno) > 0 {
		err := pushSeqnoUpdate(s, improvedSeqno)
		if err != nil {
			return err
		}
	}

	if len(retractions) > 0 {
		broadcastUpdates(s, retractions, true)
	}

	return checkStarvation(s)
}

func checkStarvation(s *state.State) error {
	r := Get[*Router](s)
	starved := false
	// check for starvation
	if time.Now().Sub(r.LastStarvationRequest) > state.StarvationDelay {
		for node, route := range r.Routes {
			neigh := s.GetNeighbour(route.Nh)
			bestMetric := state.INF
			bestEp := neigh.BestEndpoint()
			if bestEp != nil {
				bestMetric = bestEp.Metric()
			}
			if bestMetric == state.INF || route.PubMetric == state.INF {
				// we dont have a valid route to this node
				starved = true

				prev := r.SeqnoDedup.Get(node)
				if prev != nil && SeqnoGe(prev.Value().Seqno, route.Src.Seqno) {
					continue // we have already sent such a request before
				}
				r.SeqnoDedup.Set(node, route.Src, ttlcache.DefaultTTL)

				broadcastSeqnoRequest(s, mapToPktSource(&route.Src))
			}
		}
	}
	if starved {
		r.LastStarvationRequest = time.Now()
	}
	return nil
}
