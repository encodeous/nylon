package impl

import (
	"github.com/encodeous/nylon/state"
	"slices"
	"time"
)

type LinkMgr struct {
	activeLinks []state.CtlLink
}

func ProbeLinks(s *state.State) error {
	//ny := Get[*LinkMgr](s)
	rt := Get[*Router](s)
	//s.Log.Debug("Probing links", "ny", ny)

	for _, edge := range s.Edges {
		var neighNode state.Node
		if edge.V1 == s.Id {
			neighNode = edge.V2
		}
		if edge.V2 == s.Id {
			neighNode = edge.V1
		}
		if neighNode != s.Id && neighNode != "" {
			// make sure we are not already connected to the neighbour
			if !slices.ContainsFunc(rt.Neighbours, func(neighbour *state.Neighbour) bool {
				return neighbour.Id == neighNode
			}) {
				ConnectCtl(s, neighNode)
			}
		}
	}

	return nil
}

func linkHandler(e *state.Env, links <-chan state.CtlLink) {
	e.Log.Debug("link processor start")
	for link := range links {
		e.Log.Debug("link", "id", link.Id().String())
		go func() {
			cfg, err := handshake(e, link)
			if err != nil {
				link.Close()
				return
			}

			err = authenticate(e, link, cfg)
			if err != nil {
				link.Close()
				return
			}

			// we are good!
			e.Dispatch(func(s *state.State) error {
				return Get[*Router](s).AddNeighbour(s, cfg, link)
			})
		}()
	}
}

func (n *LinkMgr) Init(s *state.State) error {
	s.Log.Debug("init link manager")

	links := make(chan state.CtlLink)
	s.LinkChannel = links
	n.activeLinks = make([]state.CtlLink, 0)

	go linkHandler(s.Env, links)
	go ListenCtlTCP(s.Env, s.CtlAddr)

	// schedule timed tasks
	s.Env.RepeatTask(ProbeLinks, time.Second*5)

	return nil
}
