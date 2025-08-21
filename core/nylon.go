package core

import (
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
	"net"
	"time"
)

// Nylon struct must be thread safe, since it can receive packets through PolyReceiver
type Nylon struct {
	PingBuf *ttlcache.Cache[uint64, EpPing]
	Device  *device.Device
	Tun     tun.Device
	wgUapi  net.Listener
	env     *state.Env
	itfName string
}

func (n *Nylon) Init(s *state.State) error {
	n.env = s.Env

	s.Log.Debug("init nylon")

	// add neighbours
	for _, peer := range s.GetPeers() {
		if !s.IsRouter(peer) {
			continue
		}
		stNeigh := &state.Neighbour{
			Id: peer,
			//Routes: make(map[state.NodeId]state.PubRoute),
			//Eps:    make([]*state.NylonEndpoint, 0),
			//IO: state.IOPending{
			//	SeqnoReq: make(map[state.Source]struct{}),
			//	Updates:  make(map[state.NodeId]*protocol.Ny_Update),
			//},
		}
		cfg := s.GetRouter(peer)
		for _, ep := range cfg.Endpoints {
			stNeigh.Eps = append(stNeigh.Eps, state.NewEndpoint(ep, peer, false, nil))
		}

		s.Neighbours = append(s.Neighbours, stNeigh)
	}

	n.PingBuf = ttlcache.New[uint64, EpPing](
		ttlcache.WithTTL[uint64, EpPing](5*time.Second),
		ttlcache.WithDisableTouchOnHit[uint64, EpPing](),
	)
	go n.PingBuf.Start()

	s.Env.RepeatTask(nylonGc, state.GcDelay)
	//s.Env.RepeatTask(otelUpdate, state.OtelDelay)

	// wireguard configuration
	err := n.initWireGuard(s)
	if err != nil {
		return err
	}

	// endpoint probing
	s.Env.RepeatTask(func(s *state.State) error {
		return n.probeLinks(s, true)
	}, state.ProbeDelay)
	s.Env.RepeatTask(func(s *state.State) error {
		return n.probeLinks(s, false)
	}, state.ProbeRecoveryDelay)
	s.Env.RepeatTask(func(s *state.State) error {
		return n.probeNew(s)
	}, state.ProbeDiscoveryDelay)

	err = n.initPassiveClient(s)
	if err != nil {
		return err
	}

	// check for central config updates
	if s.Dist != nil {
		for _, repo := range s.Dist.Repos {
			s.Log.Info("config source", "repo", repo)
		}
		s.Env.RepeatTask(checkForConfigUpdates, state.CentralUpdateDelay)
	}
	return nil
}

func (n *Nylon) Cleanup(s *state.State) error {
	n.PingBuf.Stop()

	return n.cleanupWireGuard(s)
}
