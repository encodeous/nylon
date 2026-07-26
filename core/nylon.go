package core

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/encodeous/nylon/perf"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/tint"
	"github.com/gaissmai/bart"
	"github.com/jellydator/ttlcache/v3"
	slogmulti "github.com/samber/slog-multi"
)

type Nylon struct {
	Trace *NylonTrace

	// tunables and options
	state.RouterTunables
	state.NylonOptions

	// state
	state.ConfigState
	RouterState   *state.RouterState
	AppliedSystem AppliedSystemState
	PingBuf       *ttlcache.Cache[uint64, EpPing]
	PeerMap       atomic.Pointer[map[state.NyPublicKey]state.NodeId]

	// NodeIdMap maps NodeId<->binary node id, refreshed on every central
	// config apply. Read on the dataplane to encode and decode unicast
	// packet headers without referencing the live CentralCfg.
	NodeIdMap atomic.Pointer[state.NodeIdMap]

	// ExitFilter holds the immutable per-packet state needed by the exit
	// filter, snapshotted on every config apply. The filter reads only
	// this pointer, never the live CentralCfg or LocalCfg, since access
	// to those off the dispatch goroutine would otherwise require locks.
	ExitFilter atomic.Pointer[ExitFilterSnapshot]

	router struct {
		LastStarvationRequest time.Time
		IO                    map[state.NodeId]*IOPending

		// ForwardTable contains the full routing table
		ForwardTable atomic.Pointer[bart.Table[RouteTableEntry]]
		// ExitTable contains only routes to services hosted on this node
		ExitTable atomic.Pointer[bart.Table[RouteTableEntry]]
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

func NewNylon(ccfg state.CentralCfg, ncfg state.LocalCfg, logLevel slog.Level, configPath string, aux map[string]any, opts state.NylonOptions, tunables *state.RouterTunables) (*Nylon, error) {
	ctx, cancel := context.WithCancelCause(context.Background())

	dispatch := make(chan func() error, 128)

	var rt state.RouterTunables
	if tunables != nil {
		rt = *tunables
	} else {
		rt = state.DefaultRouterTunables()
	}

	handlers := make([]slog.Handler, 0)
	if opts.DBG_log_json {
		handlers = append(handlers,
			slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
				Level: logLevel,
			}),
		)
	} else {
		handlers = append(handlers,
			tint.NewHandler(os.Stderr, &tint.Options{
				Level:        logLevel,
				AddSource:    false,
				CustomPrefix: string(ncfg.Id),
				ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
					if attr.Key == "time" {
						return slog.Attr{}
					}
					return attr
				},
			}))
	}

	if ncfg.LogPath != "" {
		err := os.MkdirAll(path.Dir(ncfg.LogPath), 0600)
		if err != nil {
			return nil, err
		}
		f, err := os.OpenFile(ncfg.LogPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, slog.NewTextHandler(f, &slog.HandlerOptions{Level: logLevel}))
	}

	logger := slog.New(
		slogmulti.Fanout(handlers...))

	if ncfg.InterfaceName == "" {
		ncfg.InterfaceName = "nylon"
	}

	n := &Nylon{
		Trace:          &NylonTrace{},
		RouterTunables: rt,
		NylonOptions:   opts,
		ConfigState: state.ConfigState{
			CentralCfg: ccfg,
			LocalCfg:   ncfg,
		},
		Context:         ctx,
		Cancel:          cancel,
		DispatchChannel: dispatch,
		Log:             logger,
		ConfigPath:      configPath,
		AuxConfig:       aux,
	}

	n.Log.Info("init modules")

	err := n.Init()
	if err != nil {
		return nil, err
	}
	return n, nil
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
	err = n.reconcileRouterState(&n.CentralCfg)
	if err != nil {
		return err
	}
	if err := n.refreshNodeBindings(); err != nil {
		return err
	}

	n.PingBuf = ttlcache.New[uint64, EpPing](
		ttlcache.WithTTL[uint64, EpPing](5*time.Second),
		ttlcache.WithDisableTouchOnHit[uint64, EpPing](),
	)
	go n.PingBuf.Start()

	n.RepeatTask(func() error {
		return nylonGc(n)
	}, n.GcDelay)

	// wireguard configuration
	err = n.initWireGuard()
	if err != nil {
		return err
	}

	// endpoint probing
	n.RepeatTask(func() error {
		return n.probeLinks(true)
	}, n.ProbeDelay)
	n.RepeatTask(func() error {
		// refresh dynamic endpoints
		for _, neigh := range n.RouterState.Neighbours {
			for _, ep := range neigh.Eps {
				if nep, ok := ep.(*state.NylonEndpoint); ok {
					go func() {
						_, err := nep.DynEP.Refresh(n.EndpointResolveExpiry)
						if err != nil {
							n.Log.Debug("failed to resolve endpoint", "ep", nep.DynEP.Value, "err", err.Error())
						}
					}()
				}
			}
		}
		return nil
	}, n.EndpointResolveDelay)
	n.RepeatTask(func() error {
		return n.probeLinks(false)
	}, n.ProbeRecoveryDelay)
	n.RepeatTask(func() error {
		return n.probeNew()
	}, n.ProbeDiscoveryDelay)

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
		n.RepeatTask(func() error { return checkForConfigUpdates(n) }, n.CentralUpdateDelay)
	}
	return nil
}

func (n *Nylon) Start() error {
	n.Log.Info("init modules complete")

	n.Log.Info("Nylon has been initialized. To gracefully exit, send SIGINT or Ctrl+C.")

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case _ = <-c:
			n.Cancel(errors.New("received shutdown signal"))
		case <-n.Context.Done():
			return
		}
	}()

	err := n.mainLoop()
	if err != nil {
		return err
	}
	return nil
}

func (n *Nylon) Stop() {
	n.cleanupOnce.Do(func() {
		n.Cancel(context.Canceled)
		if n.Context.Err() == nil {
			select {
			case n.DispatchChannel <- nil: // instead of close(), which can cause a data race
			case <-n.Context.Done():
			}
		}
	})
}

func (n *Nylon) mainLoop() error {
	n.Log.Debug("started main loop")
	for {
		select {
		case fun := <-n.DispatchChannel:
			if fun == nil {
				goto endLoop
			}
			//n.Log.Debug("start")
			start := time.Now()
			err := fun()
			if err != nil {
				n.Log.Error("error occurred during dispatch: ", "error", err)
				n.Cancel(err)
			}
			elapsed := time.Since(start)
			perf.DispatchLatency.Add(float64(elapsed.Microseconds()))
			if elapsed > time.Millisecond*4 {
				n.Log.Warn("dispatch took a long time!", "fun", runtime.FuncForPC(reflect.ValueOf(fun).Pointer()).Name(), "elapsed", elapsed, "len", len(n.DispatchChannel))
			}
			//n.Log.Debug("done", "elapsed", elapsed)
		case <-n.Context.Done():
			goto endLoop
		}
	}
endLoop:
	n.Log.Info("stopped main loop", "reason", context.Cause(n.Context).Error())
	n.Stop()
	n.Log.Info("cleaning up modules")
	err := n.Cleanup()
	if err != nil {
		n.Log.Error("error occurred during Stop: ", "error", err)
	}
	n.Log.Info("stopped")
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
