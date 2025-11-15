package core

import (
	"context"
	"net"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
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

	if len(s.DnsResolvers) != 0 {
		s.Log.Debug("setting custom DNS resolvers", "resolvers", s.DnsResolvers)
		net.DefaultResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{
					Timeout: time.Second * 10,
				}
				var err error
				var conn net.Conn
				for _, resolver := range s.DnsResolvers {
					conn, err = d.DialContext(ctx, network, resolver)
					if err == nil {
						return conn, nil
					}
				}
				return conn, err
			},
		}
	}

	// add neighbours
	for _, peer := range s.GetPeers(s.Id) {
		if !s.IsRouter(peer) {
			continue
		}
		stNeigh := &state.Neighbour{
			Id:     peer,
			Routes: make(map[state.Source]state.NeighRoute),
			Eps:    make([]state.Endpoint, 0),
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

	return n.cleanupWireGuard(s)
}
