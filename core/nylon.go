package core

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/gaissmai/bart"
	"github.com/jellydator/ttlcache/v3"
)

type Nylon struct {
	Trace *NylonTrace

	// state
	state.ConfigState
	RouterState   *state.RouterState
	AppliedSystem AppliedSystemState
	PingBuf       *ttlcache.Cache[uint64, EpPing]

	router struct {
		LastStarvationRequest time.Time
		IO                    map[state.NodeId]*IOPending

		// ForwardTable contains the full routing table
		ForwardTable bart.Table[RouteTableEntry]
		// ExitTable contains only routes to services hosted on this node
		ExitTable bart.Table[RouteTableEntry]
		log       *slog.Logger
	}

	// runtime/application
	DispatchChannel chan func() error
	Log             *slog.Logger
	ConfigPath      string

	// resources
	Tun       tun.Device
	wgUapi    net.Listener
	Interface string
	Device    *device.Device

	// only used for debugging & tests
	AuxConfig map[string]any

	// lifecycle
	Context     context.Context
	Cancel      context.CancelCauseFunc
	cleanupOnce sync.Once
}

type AppliedSystemState struct {
	Routes  []netip.Prefix
	Aliases []netip.Addr
	Peers   map[state.NodeId]state.NyPublicKey
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

	if n.AppliedSystem.Peers == nil {
		n.AppliedSystem.Peers = make(map[state.NodeId]state.NyPublicKey)
	}
	err = n.reconcileRouterState(n.CentralCfg)
	if err != nil {
		return err
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

	n.startAdvertisedPrefixHealth()

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
