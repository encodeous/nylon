package core


func nylonGc(n *Nylon) error {
	s := n.State
	// scan for dead links
	for _, neigh := range s.Neighbours {
		// filter dplinks
		n := 0
		for _, x := range neigh.Eps {
			x := x.AsNylonEndpoint()
			if !x.IsActive() {
				x.DynEP.Clear()
			}
			if x.IsAlive() {
				neigh.Eps[n] = x
				n++
			} else {
				s.Log.Debug("removed dead endpoint", "ep", x.DynEP.String(), "to", neigh.Id)
			}
		}
		neigh.Eps = neigh.Eps[:n]
	}

	err := n.Router.GcRouter()
	if err != nil {
		return err
	}

	return nil
}
