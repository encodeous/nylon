package impl

import (
	"github.com/encodeous/nylon/state"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

type Nylon struct {
}

func (n *Nylon) Cleanup(s *state.State) error {
	return nil
}

func nylonGc(s *state.State) error {
	// scan for dead links
	r := Get[*Router](s)
	for _, neigh := range r.Neighbours {
		// filter ctl links
		n := 0
		for _, x := range neigh.CtlLinks {
			if x.IsAlive() {
				neigh.CtlLinks[n] = x
				n++
			} else {
				s.Log.Debug("removed dead ctllink", "id", x.Id())
			}
		}
		neigh.CtlLinks = neigh.CtlLinks[:n]

		// filter dplinks
		n = 0
		for _, x := range neigh.DpLinks {
			if x.IsAlive() {
				neigh.DpLinks[n] = x
				n++
			} else {
				s.Log.Debug("removed dead dplink", "id", x.Id(), "name", x.Endpoint().Name)
			}
		}
		neigh.DpLinks = neigh.DpLinks[:n]

		// remove old routes
		for k, x := range neigh.Routes {
			if x.LastPublished.Before(time.Now().Add(-RouteUpdateDelay * 2)) {
				s.Log.Debug("removed dead route", "src", x.Src.Id, "nh", neigh.Id)
				delete(neigh.Routes, k)
			}
		}
	}

	// cleanup link cache
	w := Get[*DpLinkMgr](s)
	for k, v := range w.endpointDiff {
		if v.V2.Before(time.Now().Add(-EndpointTTL)) {
			delete(w.endpointDiff, k)
		}
	}
	return nil
}

func otelUpdate(s *state.State) error {
	r := Get[*Router](s)

	for _, neigh := range r.Neighbours {
		for _, x := range neigh.DpLinks {
			if x.IsAlive() {
				linkMet.Record(s.Context, int64(x.Metric()),
					metric.WithAttributes(attribute.String("link.from", string(s.Id))),
					metric.WithAttributes(attribute.String("link.to", string(neigh.Id))),
					metric.WithAttributes(attribute.String("link.name", x.Endpoint().Name)),
				)
			}
		}
	}
	return nil
}

func (n *Nylon) Init(s *state.State) error {
	s.Log.Debug("init nylon")
	s.Env.RepeatTask(nylonGc, GcDelay)
	s.Env.RepeatTask(otelUpdate, OtelDelay)

	return nil
}
