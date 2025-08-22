package core

// This file makes references to RFC 8966:
// https://datatracker.ietf.org/doc/html/rfc8966

import (
	"slices"
	"time"

	"github.com/encodeous/nylon/state"
)

type RouterEvent int

// trace events

const (
	RouteImproved RouterEvent = iota
	RouteRetracted
	RouteAdded
	StaleRouteDropped
)

// warn events

const (
	InconsistentState RouterEvent = iota + 1000
	NoEpToNeighbour
)

// Router is an interface that defines the underlying router operations
type Router interface {
	SendRouteUpdate(neigh state.NodeId, svc state.ServiceId, advRoute state.PubRoute)
	SendAckRetract(neigh state.NodeId, svc state.ServiceId)
	BroadcastSendRouteUpdate(svc state.ServiceId, advRoute state.PubRoute)
	RequestSeqno(neigh state.NodeId, src state.Source, seqno uint16, hopCnt uint8)
	BroadcastRequestSeqno(src state.Source, seqno uint16, hopCnt uint8)
	Log(event RouterEvent, args ...any)
}

func updateFeasibility(router *state.RouterState, advRoute state.PubRoute) {
	// 3.7.3.  Maintaining Feasibility Distances
	//   Before sending an update (prefix, plen, router-id, seqno, metric)
	//   with finite metric (i.e., not a route retraction), a Babel node
	//   updates the feasibility distance maintained in the source table.
	//   This is done as follows.

	srcInfo, ok := router.Sources[advRoute.Source]
	if !ok {
		//   If no entry indexed by (prefix, plen, router-id) exists in the source
		//   table, then one is created with value (prefix, plen, router-id,
		//   seqno, metric).
		router.Sources[advRoute.Source] = advRoute.FD
	} else {
		//   If an entry (prefix, plen, router-id, seqno', metric') exists, then
		//   it is updated as follows:
		//
		//   *  if seqno > seqno', then seqno' := seqno, metric' := metric;
		//
		//   *  if seqno = seqno' and metric' > metric, then metric' := metric;
		//
		//   *  otherwise, nothing needs to be done.
		if SeqnoLt(srcInfo.Seqno, advRoute.Seqno) {
			srcInfo.Seqno = advRoute.Seqno
			srcInfo.Metric = advRoute.Metric
		} else if srcInfo.Seqno == advRoute.Seqno && srcInfo.Metric > advRoute.Metric {
			srcInfo.Metric = advRoute.Metric
		}
		router.Sources[advRoute.Source] = srcInfo // update the source table
	}
}

func checkFeasibility(router *state.RouterState, advRoute state.PubRoute) bool {
	// 2.4.  Feasibility Conditions and,
	// 2.5.  Solving Starvation: Sequencing Routes

	// A received update is feasible when either it is more recent than the
	//   feasibility distance maintained by the receiving node, or it is
	//   equally recent and the metric is strictly smaller.
	// More formally, if:
	//   FD(A) = (s, m), then an update carrying the distance (s', m') is
	//   feasible when either s' > s, or s = s' and m' < m.

	// NOTE: Here, we check feasibility against the advertised metric,
	// and compute the Source table using the advertised metric + link metric.

	srcInfo, ok := router.Sources[advRoute.Source]
	if !ok || SeqnoLt(srcInfo.Seqno, advRoute.Seqno) || advRoute.Metric == state.INF {
		return true
	} else if srcInfo.Seqno == advRoute.Seqno && advRoute.Metric < srcInfo.Metric {
		return true
	}
	return false
}

func RunGC(s *state.RouterState, r Router) {
	now := time.Now()

	//   When a route's expiry timer triggers, the behaviour depends on
	//   whether the route's metric is finite.  If the metric is finite, it is
	//   set to infinity and the expiry timer is reset.  If the metric is
	//   already infinite, the route is flushed from the route table.

	// scan neighbour routes for expiry
	for _, neigh := range s.Neighbours {
		for src, route := range neigh.Routes {
			if now.After(route.ExpireAt) {
				if route.Metric == state.INF {
					// route expired and is INF, delete it
					delete(neigh.Routes, src)
				} else {
					// route expired, set metric to INF
					route.Metric = state.INF
					route.ExpireAt = time.Now().Add(state.RouteExpiryTime) // reset expiry time
					neigh.Routes[src] = route                              // update the route
				}
			}
		}
	}

	// Re-run route selection
	ComputeRoutes(s, r)
}

func retract(s *state.RouterState, r Router, svc state.ServiceId) {
	tblEntry, ok := s.Routes[svc]
	if !ok {
		r.Log(InconsistentState, "attempted to retract non-existent route", "svc", svc)
		return // route does not exist
	}
	tblEntry.Metric = state.INF
	r.BroadcastSendRouteUpdate(svc, tblEntry.PubRoute)
}

func HandleSeqnoRequest(s *state.RouterState, r Router, fromNeigh state.NodeId, src state.Source, reqSeqno uint16, hopCnt uint8) {
	// 3.8.1.2.  Seqno Requests
	//
	//   When a node receives a seqno request for a given router-id and
	//   sequence number, it checks whether its route table contains a
	//   selected entry for that prefix.

	if selRoute, ok := s.Routes[src.ServiceId]; ok {
		//   If a selected route for the given prefix exists and has finite metric,
		//   and either the router-ids are different or the router-ids are equal
		//   and the entry's sequence number is no smaller (modulo 2^(16)) than
		//   the requested sequence number, the node MUST send an update for the
		//   given prefix.
		if selRoute.Metric != state.INF &&
			(selRoute.Source.NodeId != src.NodeId || selRoute.Source.NodeId == src.NodeId && SeqnoGe(selRoute.FD.Seqno, reqSeqno)) {
			updateFeasibility(s, selRoute.PubRoute)
			r.SendRouteUpdate(fromNeigh, src.ServiceId, selRoute.PubRoute)
		}
		//   If the router-ids match, but the requested seqno is larger (modulo 2^(16)) than the
		//   route entry's, the node compares the router-id against its own
		//   router-id.
		if selRoute.Source.NodeId == src.NodeId && SeqnoGt(reqSeqno, selRoute.FD.Seqno) {
			if selRoute.Source.NodeId == s.Id {
				//   If the router-id is its own, then it increases its
				//   sequence number by 1 (modulo 2^(16)) and sends an update.  A node
				//   MUST NOT increase its sequence number by more than 1 in reaction to a
				//   single seqno request.

				//   Nylon note: We increase seqno by more than one, as we do not persist our seqno
				//   state, so we cannot guarantee that increasing by one is enough.

				s.Seqno = reqSeqno
				ComputeRoutes(s, r) // should generate an update
			} else {
				//   Otherwise, if the requested router-id is not its own, the received
				//   node consults the Hop Count field of the request.  If the hop count
				//   is 2 or more, and the node is advertising the prefix to its
				//   neighbours, the node selects a neighbour to forward the request to as
				//   follows:

				_, isAdv := s.Routes[src.ServiceId]
				if hopCnt >= 2 && isAdv {
					var nh *state.NodeId
					if NeighContainsFunc(s, func(neigh state.NodeId, route state.NeighRoute) bool {
						//   *  if the node has one or more feasible routes towards the requested
						//      prefix with a next hop that is not the requesting node, then the
						//      node MUST forward the request to the next hop of one such route;
						if src.ServiceId == route.ServiceId && checkFeasibility(s, route.PubRoute) {
							nh = &neigh
							return true // found a feasible route
						}
						return false // not feasible
					}) || NeighContainsFunc(s, func(neigh state.NodeId, route state.NeighRoute) bool {
						//   *  otherwise, if the node has one or more (not feasible) routes to
						//      the requested prefix with a next hop that is not the requesting
						//      node, then the node SHOULD forward the request to the next hop of
						//      one such route.
						if src.ServiceId == route.ServiceId && neigh != fromNeigh {
							nh = &neigh
							return true // found a route
						}
						return false // not found
					}) {
						if nh != nil {
							//   In order to actually forward the request, the node decrements the hop
							//   count and sends the request in a unicast packet destined to the
							//   selected neighbour.  The forwarded request SHOULD be sent as an
							//   urgent TLV (as defined in Section 3.1).
							r.RequestSeqno(*nh, src, reqSeqno, hopCnt-1)
						}
					}
				}
			}
		}
	}

}

func HandleAckRetract(s *state.RouterState, r Router, neighId state.NodeId, svc state.ServiceId) {
	rt, ok := s.Routes[svc]
	if !ok {
		r.Log(InconsistentState, "attempted to ack the retraction of a non-existent route", "svc", svc)
		return // route does not exist
	}
	if !slices.Contains(rt.RetractedBy, neighId) {
		rt.RetractedBy = append(rt.RetractedBy, neighId)
		s.Routes[svc] = rt // update the route table
		// recompute routes
		ComputeRoutes(s, r)
	}
}

// this function should also be called on every probe
func HandleNeighbourUpdate(s *state.RouterState, r Router, neighId state.NodeId, adv state.PubRoute) {
	// 	 3.5.3.  Route Acquisition
	//
	//   When a Babel node receives an update (prefix, plen, router-id, seqno,
	//   metric) from a neighbour neigh, it checks whether it already has a
	//   route table entry indexed by (prefix, plen, neigh).

	n := s.GetNeighbour(neighId)

	_, ok := n.Routes[adv.Source]

	if adv.Metric == state.INF {
		r.SendAckRetract(neighId, adv.Source.ServiceId)
	}

	if !ok {
		//    	If no such entry exists:
		//
		//   *  if the update is unfeasible, it MAY be ignored;

		if !checkFeasibility(s, adv) {
			return // this route is not feasible, ignored
		}

		//   *  if the metric is infinite (the update is a retraction of a route
		//      we do not know about), the update is ignored;

		if adv.Metric == state.INF {
			return // ignored as the metric is infinite
		}

		//   *  otherwise, a new entry is created in the route table, indexed by
		//      (prefix, plen, neigh), with source equal to (prefix, plen, router-
		//      id), seqno equal to seqno, and an advertised metric equal to the
		//      metric carried by the update.

		// create the route
		n.Routes[adv.Source] = state.NeighRoute{
			PubRoute: adv,
			ExpireAt: time.Now().Add(state.RouteExpiryTime),
		}
	} else {
		// 		If such an entry exists:
		//
		//   *  if the entry is currently selected, the update is unfeasible, and
		//      the router-id of the update is equal to the router-id of the
		//      entry, then the update MAY be ignored;

		selRoute, hasSelected := s.Routes[adv.Source.ServiceId]
		isSelected := hasSelected && selRoute.Nh == neighId
		if !checkFeasibility(s, adv) {
			isMoreOptimal := hasSelected && ShouldSwitch(selRoute, state.SelRoute{
				PubRoute: adv,
				Nh:       neighId,
				ExpireAt: time.Time{},
			})
			if isSelected {
				// 3.8.2.2.  Dealing with Unfeasible Updates
				//   In order to keep routes from spuriously expiring because they have
				//   become unfeasible, a node SHOULD send a unicast seqno request when it
				//   receives an unfeasible update for a route that is currently selected.
				//   The requested sequence number is computed from the source table as in
				//   Section 3.8.2.1.
				r.RequestSeqno(neighId, adv.Source, s.Sources[adv.Source].Seqno+1, state.SeqnoRequestHopCount)
				return // ignore the unfeasible update, we are conservative with retractions here
			} else if isMoreOptimal {
				//   Additionally, since metric computation does not necessarily coincide
				//   with the delay in propagating updates, a node might receive an
				//   unfeasible update from a currently unselected neighbour that would
				//   lead to the received route becoming selected were it feasible. In that
				//   case, the node SHOULD send a unicast seqno request to the neighbour
				//   that advertised the preferable update.
				r.RequestSeqno(neighId, adv.Source, s.Sources[adv.Source].Seqno+1, state.SeqnoRequestHopCount)
			}
		}

		//   *  otherwise, the entry's sequence number, advertised metric, metric,
		//      and router-id are updated, and if the advertised metric is not
		//      infinite, the route's expiry timer is reset to a small multiple of
		//      the interval value included in the update (see "Route Expiry time"
		//      in Appendix B for suggested values).  If the update is unfeasible,
		//      then the (now unfeasible) entry MUST be immediately unselected.
		//      If the update caused the router-id of the entry to change, an
		//      update (possibly a retraction) MUST be sent in a timely manner as
		//      described in Section 3.7.2.

		nr := n.Routes[adv.Source]
		nr.PubRoute = adv

		if adv.Metric != state.INF {
			nr.ExpireAt = time.Now().Add(state.RouteExpiryTime)
		}
		n.Routes[adv.Source] = nr
	}
}

func isHeldRoute(s *state.RouterState, route state.SelRoute) bool {
	if route.Nh == s.Id {
		return false // we do not hold routes to ourselves
	}
	connectedNeighs := 0
	for _, neigh := range s.Neighbours {
		if neigh.BestEndpoint() != nil {
			connectedNeighs++
		}
	}
	neigh := s.GetNeighbour(route.Nh)
	hasLink := neigh != nil && neigh.BestEndpoint() != nil
	return (route.Metric == state.INF || !hasLink) &&
		len(route.RetractedBy) < connectedNeighs &&
		route.ExpireAt.After(time.Now())
}

func ComputeRoutes(s *state.RouterState, r Router) {
	newTable := make(map[state.ServiceId]state.SelRoute)

	// 3.5.4.  Hold Time
	//
	//   When a prefix P is retracted (because all routes are unfeasible or
	//   have an infinite metric, whether due to the expiry timer or for other
	//   reasons), and a shorter prefix P' that covers P is reachable, P'
	//   cannot in general be used for routing packets destined to P without
	//   running the risk of creating a routing loop (Section 2.8).
	//
	//   To avoid this issue, whenever a prefix P is retracted, a route table
	//   entry with infinite metric is maintained as described in
	//   Section 3.5.3.  As long as this entry is maintained, packets destined
	//   to an address within P MUST NOT be forwarded by following a route for
	//   a shorter prefix.  This entry is removed as soon as a finite-metric
	//   update for prefix P is received and the resulting route selected.  If
	//   no such update is forthcoming, the infinite metric entry SHOULD be
	//   maintained at least until it is guaranteed that no neighbour has
	//   selected the current node as next hop for prefix P.  This can be
	//   achieved by either:
	//
	//   *  waiting until the route's expiry timer has expired
	//      (Section 3.5.3); or
	//
	//   *  sending a retraction with an acknowledgment request (Section 3.3)
	//      to every reachable neighbour that has not explicitly retracted
	//      prefix P, and waiting for all acknowledgments.
	//
	//   The former option is simpler and ensures that, at that point, any
	//   routes for prefix P pointing at the current node have expired.
	//   However, since the expiry time can be as high as a few minutes, doing
	//   that prevents automatic aggregation by creating spurious black-holes
	//   for aggregated routes.  The latter option is RECOMMENDED as it
	//   dramatically reduces the time for which a prefix is unreachable in
	//   the presence of aggregated routes.

	// In order to avoid routing loops, we pick the second option. Here, we will re-introduce held routes

	for svc, route := range s.Routes {
		if isHeldRoute(s, route) {
			newTable[svc] = route
		}
	}

	// 3.6.  Route Selection
	//   Route selection is the process by which a single route for a given
	//   prefix is selected to be used for forwarding packets and to be re-
	//   advertised to a node's neighbours.

	//   Babel is designed to allow flexible route selection policies.  As far
	//   as the algorithm's correctness is concerned, the route selection
	//   policy MUST only satisfy the following properties:
	//
	//   *  a route with infinite metric (a retracted route) is never
	//      selected;
	//
	//   *  an unfeasible route is never selected.

	//   Route selection MUST NOT take seqnos into account: a route MUST NOT
	//   be preferred just because it carries a higher (more recent) seqno.
	//   Doing otherwise would cause route oscillation while a new seqno
	//   propagates across the network, and might create persistent black-
	//   holes if the metric being used is not left-distributive
	//   (Section 3.5.2).

	// enumerate through neighbours
	for _, neigh := range s.Neighbours {
		bestEp := neigh.BestEndpoint()
		if bestEp == nil {
			r.Log(NoEpToNeighbour, "no endpoint to neighbour", "neigh", neigh.Id)
		}

		// We refer to our current node as A, our neighbour as B, and S as our source.

		// Cost(A, B)
		CAB := state.INF

		if bestEp != nil {
			CAB = bestEp.Metric()
			CAB = AddMetric(CAB, state.HopCost) // to prevent 0 cost metric
		}

		// enumerate through neighbour advertisements
		for S, adv := range neigh.Routes {
			if slices.ContainsFunc(s.Advertised, func(s state.ServiceId) bool {
				return s == S.ServiceId
			}) {
				continue // skip self routes
			}
			svc := S.ServiceId

			// Cost(A, B) + Cost(S, B)
			totalCost := AddMetric(CAB, adv.Metric)

			oldRoute, exists := newTable[svc]

			retractedBy := make([]state.NodeId, 0)
			if exists {
				retractedBy = oldRoute.RetractedBy
			}

			fd := state.FD{
				Seqno:  adv.Seqno,
				Metric: totalCost,
			}
			newRoute := state.SelRoute{
				PubRoute: state.PubRoute{
					Source: adv.Source,
					FD:     fd,
				},
				Nh:          neigh.Id,
				ExpireAt:    adv.ExpireAt,
				RetractedBy: retractedBy,
			}

			// update the route if it is currently selected
			if exists && oldRoute.Nh == newRoute.Nh {
				newTable[svc] = newRoute
			}

			//   *  a route with infinite metric (a retracted route) is never
			//      selected;
			if totalCost == state.INF {
				continue // ignored
			}

			//   *  an unfeasible route is never selected.
			if !checkFeasibility(s, adv.PubRoute) {
				continue // ignored
			}

			if !exists {
				// create new route
				newTable[svc] = newRoute
			} else {
				// check if we should switch to this route
				if ShouldSwitch(oldRoute, newRoute) && !isHeldRoute(s, oldRoute) {
					newTable[svc] = newRoute
				}
			}
		}
	}

	// add our own routes to the route table, so that we can advertise them
	for _, adv := range s.Advertised {
		newTable[adv] = state.SelRoute{
			PubRoute: state.PubRoute{
				Source: state.Source{
					NodeId:    s.Id,
					ServiceId: adv,
				},
				FD: state.FD{
					Seqno:  s.Seqno,
					Metric: 0,
				},
			},
			Nh:       s.Id, // next hop is self
			ExpireAt: time.Now().Add(state.RouteExpiryTime),
		}
	}

	// compare our new route table with the old one

	//   A change of router-id for the selected route to a given prefix may be
	//   indicative of a routing loop in formation; hence, whenever it changes
	//   the selected router-id for a given destination, a node MUST send an
	//   update as an urgent TLV (as defined in Section 3.1)

	// Here, we also want to send updates for new seqno, and routes that changed drastically in metric

	for svc, newRoute := range newTable {
		oldRoute, exists := s.Routes[svc]
		if !exists ||
			oldRoute.Source.NodeId != newRoute.Source.NodeId ||
			oldRoute.FD.Seqno != newRoute.FD.Seqno ||
			(newRoute.Metric-oldRoute.Metric) > state.LargeChangeThreshold && newRoute.Metric != state.INF {
			// criteria met, send update
			updateFeasibility(s, newRoute.PubRoute)
			r.BroadcastSendRouteUpdate(svc, newRoute.PubRoute)
		}
	}

	// scan for retractions
	for svc, oldRoute := range s.Routes {
		route, exists := newTable[svc]
		if !exists || route.Metric == state.INF {
			// route is no longer reachable, retract it
			if oldRoute.Metric != state.INF {
				retract(s, r, svc)
			}
		}
	}

	s.Routes = newTable // update the route table
}

func SolveStarvation(router *state.RouterState, r Router) {
	// 3.8.2.1.  Avoiding Starvation

	//   When a route is retracted or expires, a Babel node usually switches
	//   to another feasible route for the same prefix.  It may be the case,
	//   however, that no such routes are available.
	//
	//   A node that has lost all feasible routes to a given destination but
	//   still has unexpired unfeasible routes to that destination MUST send a
	//   seqno request; if it doesn't have any such routes, it MAY still send
	//   a seqno request.

	isFeasible := make(map[state.Source]bool)

	for _, neigh := range router.Neighbours {
		if neigh.BestEndpoint() == nil {
			continue // no endpoint to this neighbour
		}
		for _, route := range neigh.Routes {
			curFeasible, present := isFeasible[route.Source]
			isFeasible[route.Source] = (present && curFeasible) || checkFeasibility(router, route.PubRoute)
		}
	}

	//
	//   The router-id of the request is set to the router-
	//   id of the route that it has just lost, and the requested seqno is the
	//   value contained in the source table plus 1.  The request carries a
	//   hop count, which is used as a last-resort mechanism to ensure that it
	//   eventually vanishes from the network; it MAY be set to any value that
	//   is larger than the diameter of the network (64 is a suitable default
	//   value).
	//
	//   If the node has any (unfeasible) routes to the requested destination,
	//   then it MUST send the request to at least one of the next-hop
	//   neighbours that advertised these routes, and SHOULD send it to all of
	//   them; in any case, it MAY send the request to any other neighbours,
	//   whether they advertise a route to the requested destination or not.
	//   A simple implementation strategy is therefore to unconditionally
	//   multicast the request over all interfaces.

	for src, feasible := range isFeasible {
		if !feasible && src.NodeId != router.Id {
			r.BroadcastRequestSeqno(src, router.Sources[src].Seqno+1, state.SeqnoRequestHopCount)
		}
	}

	//   Similar requests will be sent by other nodes that are affected by the
	//   route's loss.  If the network is still connected, and assuming no
	//   packet loss, then at least one of these requests will be forwarded to
	//   the source, resulting in a route being advertised with a new sequence
	//   number.  (Due to duplicate suppression, only a small number of such
	//   requests are expected to actually reach the source.)
}

func ShouldSwitch(curRoute state.SelRoute, newRoute state.SelRoute) bool {
	// TODO: Investigate stable routing heuristics
	curMetric := float64(curRoute.Metric)
	newMetric := float64(newRoute.Metric)
	if newMetric > curMetric {
		return false
	}
	return true
}
