package core

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/gaissmai/bart"
	"github.com/jellydator/ttlcache/v3"
)

type Nylon struct {
	Trace       *NylonTrace
	RouterState *state.RouterState

	router struct {
		LastStarvationRequest time.Time
		IO                    map[state.NodeId]*IOPending

		// ForwardTable contains the full routing table
		ForwardTable bart.Table[RouteTableEntry]
		// ExitTable contains only routes to services hosted on this node
		ExitTable bart.Table[RouteTableEntry]
		log       *slog.Logger
	}

	DispatchChannel chan func() error
	state.ConfigState
	Context    context.Context
	Cancel     context.CancelCauseFunc
	Log        *slog.Logger
	AuxConfig  map[string]any
	Updating   atomic.Bool
	Stopping   atomic.Bool
	Started    atomic.Bool
	ConfigPath string

	PingBuf             *ttlcache.Cache[uint64, EpPing]
	Device              *device.Device
	Tun                 tun.Device
	wgUapi              net.Listener
	itfName             string
	prevInstalledRoutes []netip.Prefix
}

func (n *Nylon) Init() error {
	n.Log.Debug("init nylon")

	err := n.Trace.Init(n)
	if err != nil {
		return err
	}
	err = n.InitRouter()
	if err != nil {
		return err
	}

	state.SetResolvers(n.DnsResolvers)

	// add neighbours
	for _, peer := range n.GetPeers(n.LocalCfg.Id) {
		if !n.IsRouter(peer) {
			continue
		}
		stNeigh := &state.Neighbour{
			Id:     peer,
			Routes: make(map[netip.Prefix]state.NeighRoute),
			Eps:    make([]state.Endpoint, 0),
		}
		cfg := n.GetRouter(peer)
		for _, ep := range cfg.Endpoints {
			stNeigh.Eps = append(stNeigh.Eps, state.NewEndpoint(ep, false, nil))
		}

		n.RouterState.Neighbours = append(n.RouterState.Neighbours, stNeigh)
	}

	n.PingBuf = ttlcache.New[uint64, EpPing](
		ttlcache.WithTTL[uint64, EpPing](5*time.Second),
		ttlcache.WithDisableTouchOnHit[uint64, EpPing](),
	)
	go n.PingBuf.Start()

	n.RepeatTask(func() error {
		return nylonGc(n)
	}, state.GcDelay)

	// wireguard configuration
	err = n.initWireGuard()
	if err != nil {
		return err
	}

	// endpoint probing
	n.RepeatTask(func() error {
		return n.probeLinks(true)
	}, state.ProbeDelay)
	n.RepeatTask(func() error {
		// refresh dynamic endpoints
		for _, neigh := range n.RouterState.Neighbours {
			for _, ep := range neigh.Eps {
				if nep, ok := ep.(*state.NylonEndpoint); ok {
					go func() {
						_, err := nep.DynEP.Refresh()
						if err != nil {
							n.Log.Debug("failed to resolve endpoint", "ep", nep.DynEP.Value, "err", err.Error())
						}
					}()
				}
			}
		}
		return nil
	}, state.EndpointResolveDelay)
	n.RepeatTask(func() error {
		return n.probeLinks(false)
	}, state.ProbeRecoveryDelay)
	n.RepeatTask(func() error {
		return n.probeNew()
	}, state.ProbeDiscoveryDelay)

	// prefix healthcheck
	for _, ph := range n.GetNode(n.LocalCfg.Id).Prefixes {
		n.Log.Info("starting prefix healthcheck", "prefix", ph.GetPrefix())
		ph.Start(n.Log)
	}

	err = n.initPassiveClient()
	if err != nil {
		return err
	}

	// check for central config updates
	if n.CentralCfg.Dist != nil {
		for _, repo := range n.CentralCfg.Dist.Repos {
			n.Log.Info("config source", "repo", repo)
		}
		n.RepeatTask(func() error { return checkForConfigUpdates(n) }, state.CentralUpdateDelay)
	}
	return nil
}

func (n *Nylon) Cleanup() error {
	n.PingBuf.Stop()
	for _, ph := range n.GetNode(n.LocalCfg.Id).Prefixes {
		ph.Stop()
	}

	n.CleanupRouter()
	n.Trace.Cleanup()

	return n.cleanupWireGuard()
}
