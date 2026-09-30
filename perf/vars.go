package perf

import (
	"expvar"
	"net/http"

	"github.com/encodeous/metric"
)

var (
	DispatchLatency = metric.NewHistogram("1m1s")
)

func init() {
	http.Handle("/debug/metrics", metric.Handler(metric.Exposed))

	expvar.Publish("nylon:DispatchLatency (µs)", DispatchLatency)
}
