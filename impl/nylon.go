package impl

import (
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/device"
	"github.com/jellydator/ttlcache/v3"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"time"
)

var (
	otelMet = otel.Meter("encodeous.ca/nylon/metric")
	otelLog = otelslog.NewLogger("encodeous.ca/nylon/route")

	linkMet, _ = otelMet.Int64Gauge("link.metric",
		metric.WithDescription("The adjusted metric for each link"),
		metric.WithUnit("{met}"))
)

// Nylon struct must be thread safe, since it can receive packets through PolyReceiver
type Nylon struct {
	PolySock *device.PolySock
	PingBuf  *ttlcache.Cache[uint64, EpPing]
	Device   *device.Device
	env      *state.Env
	itfName  string
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
			Id:     peer,
			Routes: make(map[state.NodeId]state.PubRoute),
			Eps:    make([]*state.DynamicEndpoint, 0),
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
	s.Env.RepeatTask(otelUpdate, state.OtelDelay)

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
	}, state.DiscoveryDelay)

	err = n.initPassiveClient(s)
	if err != nil {
		return err
	}

	return nil
}

func (n *Nylon) Cleanup(s *state.State) error {
	n.PingBuf.Stop()

	return n.cleanupWireGuard(s)
}
