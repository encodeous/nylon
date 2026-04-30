package core

import (
	"github.com/dustin/go-broadcast"
)
type NylonTrace struct {
	broadcast.Broadcaster
}

func (n *NylonTrace) Init(core *Nylon) error {
	n.Broadcaster = broadcast.NewBroadcaster(1024)
	return nil
}

func (n *NylonTrace) Cleanup() error {
	return n.Broadcaster.Close()
}
