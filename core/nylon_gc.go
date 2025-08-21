package core

import (
	"github.com/encodeous/nylon/state"
)

func nylonGc(s *state.State) error {
	//// scan for dead links
	//for _, neigh := range s.Neighbours {
	//	// filter dplinks
	//	n := 0
	//	for _, x := range neigh.Eps {
	//		if x.IsAlive() {
	//			neigh.Eps[n] = x
	//			n++
	//		} else {
	//			s.Log.Debug("removed dead endpoint", "ep", x.NetworkEndpoint().Ep, "to", x.Node())
	//		}
	//	}
	//	neigh.Eps = neigh.Eps[:n]
	//
	//	// remove old routes
	//	for k, x := range neigh.Routes {
	//		if x.LastPublished.Before(time.Now().Add(-state.RouteUpdateDelay * 2)) {
	//			s.Log.Debug("removed dead route", "src", x.Src.Id, "nh", neigh.Id)
	//			delete(neigh.Routes, k)
	//		}
	//	}
	//}
	//
	//err := cleanPassivePeers(s)
	//if err != nil {
	//	return err
	//}
	return nil
}
