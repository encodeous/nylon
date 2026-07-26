package core

func nylonGc(n *Nylon) error {
	activeAddresses := make(map[string]struct{})
	for _, neigh := range n.RouterState.Neighbours {
		for _, endpoint := range neigh.Eps {
			link := endpoint.AsNylonEndpoint()
			if link.IsActive() {
				activeAddresses[link.Address] = struct{}{}
			}
		}
	}

	// scan for dead links
	for _, neigh := range n.RouterState.Neighbours {
		// filter dplinks
		count := 0
		for _, x := range neigh.Eps {
			x := x.AsNylonEndpoint()
			if !x.IsActive() {
				if _, activeElsewhere := activeAddresses[x.Address]; !activeElsewhere {
					n.EndpointResolver.Expire(x.Address)
				}
			}
			if x.IsAlive() {
				neigh.Eps[count] = x
				count++
			} else {
				n.Log.Debug("removed dead endpoint", "ep", x.Address, "to", neigh.Id)
			}
		}
		neigh.Eps = neigh.Eps[:count]
	}

	err := n.GcRouter()
	if err != nil {
		return err
	}

	return nil
}
