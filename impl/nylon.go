package impl

import (
	"github.com/encodeous/nylon/state"
	"time"
)

type Nylon struct {
	activeLinks []state.CtlLink
}

func ProbeLinks(s state.State) error {
	ny := Get[*Nylon](s)
	s.Log.Debug("Probing links", "ny", ny)

	return nil
}

func linkProcessor(e state.Env, links <-chan state.CtlLink) {
	e.Log.Debug("link processor start")
	for link := range links {
		e.Log.Debug("inbound link", "link", link)
	}
}

func scheduleTimedTasks(s state.State) {
	s.Env.RepeatTask(ProbeLinks, time.Second*5)
}

func (n *Nylon) Init(s state.State) error {
	s.Log.Debug("init nylon")

	links := make(chan state.CtlLink)
	s.LinkChannel = links
	n.activeLinks = make([]state.CtlLink, 0)

	go linkProcessor(s.Env, links)
	go ListenCtlTCP(s.Env, s.NCfg.CtlAddr)
	scheduleTimedTasks(s)

	return nil
}
