package impl

import (
	"github.com/encodeous/nylon/state"
	"time"
)

type Nylon struct {
}

func (n *Nylon) Cleanup(s *state.State) error {
	return nil
}

func nylonGc(s *state.State) error {
	// scan for dead links
	r := Get[*Router](s)
	for _, neigh := range r.Neighbours {
		// filter ctl links
		n := 0
		for _, x := range neigh.CtlLinks {
			if !x.IsDead() {
				neigh.CtlLinks[n] = x
				n++
			} else {
				s.Log.Debug("removed dead ctllink", "id", x.Id())
			}
		}
		neigh.CtlLinks = neigh.CtlLinks[:n]

		// filter dplinks
		n = 0
		for _, x := range neigh.DpLinks {
			if !x.IsDead() {
				neigh.DpLinks[n] = x
				n++
			} else {
				s.Log.Debug("removed dead dplink", "id", x.Id(), "name", x.Endpoint().Name)
			}
		}
		neigh.DpLinks = neigh.DpLinks[:n]

		// remove old routes
		for k, x := range neigh.Routes {
			if x.LastPublished.Before(time.Now().Add(-RouteUpdateDelay * 2)) {
				s.Log.Debug("removed dead route", "src", x.Src.Id, "nh", neigh.Id)
				delete(neigh.Routes, k)
			}
		}
	}
	return nil
}

func (n *Nylon) Init(s *state.State) error {
	s.Log.Debug("init nylon")
	s.Env.RepeatTask(nylonGc, GcDelay)

	return nil
}
