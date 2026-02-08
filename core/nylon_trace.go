package core

import (
	"github.com/dustin/go-broadcast"
	"github.com/encodeous/nylon/state"
)

type NylonTrace struct {
	broadcast.Broadcaster
}

func (n *NylonTrace) Init(s *state.State) error {
	n.Broadcaster = broadcast.NewBroadcaster(1024)
	return nil
}

func (n *NylonTrace) Cleanup(s *state.State) error {
	return n.Broadcaster.Close()
}
