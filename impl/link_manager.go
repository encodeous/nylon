package impl

import (
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"log/slog"
	"slices"
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
				return neighbour.Id == neighNode && len(neighbour.CtlLinks) != 0
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
				return AddNeighbour(s, cfg, link)
			})

			for e.Context.Err() == nil {
				msg := &protocol.CtlMsg{}
				err := link.ReadMsg(msg)
				if err != nil {
					goto end
				}
				packetHandler(e, msg, cfg.Id)
			}
		end:
			link.Close()
			e.Dispatch(func(s *state.State) error {
				RemoveLink(s, cfg, link)
				return nil
			})

		}()
	}
}

func packetHandler(e *state.Env, pkt *protocol.CtlMsg, node state.Node) {
	e.Dispatch(func(s *state.State) error {
		switch pkt.Type.(type) {
		case *protocol.CtlMsg_Seqno:
			slog.Info("seqno request", "pkt", pkt.GetSeqno())
			break
		case *protocol.CtlMsg_Route:
			return routerHandleRouteUpdate(s, node, pkt.GetRoute())

		}
		return nil
	})
}

func (n *LinkMgr) Init(s *state.State) error {
	s.Log.Debug("init link manager")

	links := make(chan state.CtlLink)
	s.LinkChannel = links
	n.activeLinks = make([]state.CtlLink, 0)

	go linkHandler(s.Env, links)
	go ListenCtlTCP(s.Env, s.CtlAddr)

	// schedule timed tasks
	s.Env.RepeatTask(ProbeLinks, ProbeLinkDelay)

	return nil
}
