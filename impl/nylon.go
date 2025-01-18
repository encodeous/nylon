package impl

import (
	state2 "github.com/encodeous/nylon/state"
	"time"
)

type Nylon struct {
	activeLinks []state2.CtlLink
}

func ProbeLinks(s state2.State) error {
	ny := Get[*Nylon](s)
	s.Log.Debug("Probing links", "ny", ny)

	return nil
}

func linkProcessor(e state2.Env, links <-chan state2.CtlLink) {
	e.Log.Debug("link processor start")
	//for link := range links {
	//}
}

func scheduleTimedTasks(s state2.State) {
	s.Env.RepeatTask(ProbeLinks, time.Second*5)
}

func (n *Nylon) Init(s state2.State) error {
	s.Log.Debug("init nylon")

	links := make(chan state2.CtlLink)
	s.LinkChannel = links
	n.activeLinks = make([]state2.CtlLink, 0)

	go linkProcessor(s.Env, links)
	go ListenCtlTCP(s.Env, s.NCfg.CtlAddr)
	scheduleTimedTasks(s)

	return nil
}
