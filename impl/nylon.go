package impl

import (
	"github.com/encodeous/nylon/state"
)

type Nylon struct {
}

func (n *Nylon) Init(s *state.State) error {
	s.Log.Debug("init nylon")

	return nil
}
