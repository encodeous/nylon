package impl

import (
	"github.com/encodeous/nylon/state"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

func otelUpdate(s *state.State) error {
	for _, neigh := range s.Neighbours {
		for _, x := range neigh.Eps {
			if x.IsActive() && state.OtelEnabled {
				linkMet.Record(s.Context, int64(x.Metric()),
					metric.WithAttributes(attribute.String("link.from", string(s.Id))),
					metric.WithAttributes(attribute.String("link.to", string(neigh.Id))),
				)
			}
		}
	}
	return nil
}
