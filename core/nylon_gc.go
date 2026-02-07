package core

import (
	"github.com/encodeous/nylon/state"
)

func nylonGc(s *state.State) error {
	// scan for dead links
	for _, neigh := range s.Neighbours {
		// filter dplinks
		n := 0
		for _, x := range neigh.Eps {
			x := x.AsNylonEndpoint()
			if x.IsAlive() {
				neigh.Eps[n] = x
				n++
			} else {
				s.Log.Debug("removed dead endpoint", "ep", x.DynEP.String(), "to", neigh.Id)
			}
		}
		neigh.Eps = neigh.Eps[:n]
	}

	r := Get[*NylonRouter](s)
	err := r.GcRouter(s)
	if err != nil {
		return err
	}

	return nil
}
