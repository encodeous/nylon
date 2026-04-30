package core

func nylonGc(n *Nylon) error {
	// scan for dead links
	for _, neigh := range n.RouterState.Neighbours {
		// filter dplinks
		count := 0
		for _, x := range neigh.Eps {
			x := x.AsNylonEndpoint()
			if !x.IsActive() {
				x.DynEP.Clear()
			}
			if x.IsAlive() {
				neigh.Eps[count] = x
				count++
			} else {
				n.Log.Debug("removed dead endpoint", "ep", x.DynEP.String(), "to", neigh.Id)
			}
		}
		neigh.Eps = neigh.Eps[:count]
	}

	err := n.Router.GcRouter()
	if err != nil {
		return err
	}

	return nil
}
