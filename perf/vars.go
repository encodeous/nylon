package perf

import (
	"expvar"
	"net/http"

	"github.com/encodeous/metric"
)

var (
	DispatchLatency     = metric.NewHistogram("1m1s")
	SendBatchSize       = metric.NewHistogram("10s1s")
	RecvBatchSize       = metric.NewHistogram("10s1s")
	SendsPerSecond      = metric.NewCounter("10s1s")
	RecvsPerSecond      = metric.NewCounter("10s1s")
	SentPacketPerSecond = metric.NewCounter("10s1s")
	RecvPacketPerSecond = metric.NewCounter("10s1s")
	SentBytesPerSecond  = metric.NewCounter("10s1s")
	RecvBytesPerSecond  = metric.NewCounter("10s1s")
)

func init() {
	http.Handle("/debug/metrics", metric.Handler(metric.Exposed))
	expvar.Publish("nylon:SendBatchSize", SendBatchSize)
	expvar.Publish("nylon:RecvBatchSize", RecvBatchSize)

	expvar.Publish("nylon:SentPacket/s", SentPacketPerSecond)
	expvar.Publish("nylon:RecvPacket/s", RecvPacketPerSecond)
	expvar.Publish("nylon:Sends/s", SendsPerSecond)
	expvar.Publish("nylon:Recvs/s", RecvsPerSecond)
	expvar.Publish("nylon:SentBytes/s", SentBytesPerSecond)
	expvar.Publish("nylon:RecvBytes/s", RecvBytesPerSecond)
	expvar.Publish("nylon:DispatchLatency (µs)", DispatchLatency)
}
