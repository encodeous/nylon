package impl

import (
	"github.com/encodeous/nylon/nylon_dp"
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
	PolySock  *device.PolySock
	PingBuf   *ttlcache.Cache[uint64, EpPing]
	WgDevice  *device.Device
	dataplane nylon_dp.NyItf
	env       *state.Env
}

func (n *Nylon) Init(s *state.State) error {
	n.env = s.Env

	s.Log.Debug("init nylon")

	// add neighbours
	for _, neigh := range s.GetPeers() {
		stNeigh := &state.Neighbour{
			Id:     neigh,
			Routes: make(map[state.Node]state.PubRoute),
			Eps:    make([]*state.DynamicEndpoint, 0),
		}
		cfg := s.MustGetNode(neigh)
		for _, ep := range cfg.DpAddr {
			stNeigh.Eps = append(stNeigh.Eps, state.NewUdpDpLink(ep, neigh))
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
	}, state.ProbeDpDelay)
	s.Env.RepeatTask(func(s *state.State) error {
		return n.probeLinks(s, false)
	}, state.ProbeDpInactiveDelay)

	return nil
}

func (n *Nylon) Cleanup(s *state.State) error {
	n.PingBuf.Stop()

	return n.cleanupWireGuard(s)
}
