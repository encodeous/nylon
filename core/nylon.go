package core

import (
	"net"
	"net/netip"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
)

// Nylon struct must be thread safe, since it can receive packets through PolyReceiver
type Nylon struct {
	PingBuf             *ttlcache.Cache[uint64, EpPing]
	Device              *device.Device
	Tun                 tun.Device
	wgUapi              net.Listener
	env                 *state.Env
	itfName             string
	prevInstalledRoutes []netip.Prefix
}

func (n *Nylon) Init(s *state.State) error {
	n.env = s.Env

	s.Log.Debug("init nylon")

	state.SetResolvers(s.DnsResolvers)

	// add neighbours
	for _, peer := range s.GetPeers(s.Id) {
		if !s.IsRouter(peer) {
			continue
		}
		stNeigh := &state.Neighbour{
			Id:     peer,
			Routes: make(map[netip.Prefix]state.NeighRoute),
			Eps:    make([]state.Endpoint, 0),
		}
		cfg := s.GetRouter(peer)
		for _, ep := range cfg.Endpoints {
			stNeigh.Eps = append(stNeigh.Eps, state.NewEndpoint(ep, false, nil))
		}

		s.Neighbours = append(s.Neighbours, stNeigh)
	}

	n.PingBuf = ttlcache.New[uint64, EpPing](
		ttlcache.WithTTL[uint64, EpPing](5*time.Second),
		ttlcache.WithDisableTouchOnHit[uint64, EpPing](),
	)
	go n.PingBuf.Start()

	s.Env.RepeatTask(nylonGc, state.GcDelay)

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
		// refresh dynamic endpoints
		for _, neigh := range s.Neighbours {
			for _, ep := range neigh.Eps {
				if nep, ok := ep.(*state.NylonEndpoint); ok {
					go func() {
						_, err := nep.DynEP.Refresh()
						if err != nil {
							s.Log.Debug("failed to resolve endpoint", "ep", nep.DynEP.Value, "err", err.Error())
						}
					}()
				}
			}
		}
		return nil
	}, state.EndpointResolveDelay)
	s.Env.RepeatTask(func(s *state.State) error {
		return n.probeLinks(s, false)
	}, state.ProbeRecoveryDelay)
	s.Env.RepeatTask(func(s *state.State) error {
		return n.probeNew(s)
	}, state.ProbeDiscoveryDelay)

	// prefix healthcheck
	for _, ph := range s.GetNode(s.Id).Prefixes {
		s.Log.Info("starting prefix healthcheck", "prefix", ph.GetPrefix())
		ph.Start(s.Log)
	}

	err = n.initPassiveClient(s)
	if err != nil {
		return err
	}

	// check for central config updates
	if s.CentralCfg.Dist != nil {
		for _, repo := range s.CentralCfg.Dist.Repos {
			s.Log.Info("config source", "repo", repo)
		}
		s.Env.RepeatTask(checkForConfigUpdates, state.CentralUpdateDelay)
	}
	return nil
}

func (n *Nylon) Cleanup(s *state.State) error {
	n.PingBuf.Stop()
	for _, ph := range s.GetNode(s.Id).Prefixes {
		ph.Stop()
	}

	return n.cleanupWireGuard(s)
}
