package state

import "testing"

func TestGetNeighbour(t *testing.T) {
	n1 := &Neighbour{Id: "node1"}
	n2 := &Neighbour{Id: "node2"}
	st := &State{
		RouterState: &RouterState{
			Neighbours: []*Neighbour{n1, n2},
		},
	}

	if got := st.GetNeighbour("node1"); got != n1 {
		t.Errorf("Expected neighbour node1 to be %+v, got %+v", n1, got)
	}
	if got := st.GetNeighbour("node2"); got != n2 {
		t.Errorf("Expected neighbour node2 to be %+v, got %+v", n2, got)
	}
	if got := st.GetNeighbour("node3"); got != nil {
		t.Errorf("Expected nil for missing neighbour node3, got %+v", got)
	}
}
