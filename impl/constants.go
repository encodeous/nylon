package impl

import "time"

const (
	INF                            = (uint16)(65535)
	ProbeCtlDelay                  = time.Second * 5
	RouteUpdateDelay               = time.Second * 5
	ProbeDpDelay                   = time.Millisecond * 2000
	LinkSwitchMetricCostBase       = time.Microsecond * 500
	LinkSwitchMetricCostMultiplier = 1.3
	StarvationDelay                = time.Millisecond * 400

	// sliding window latency
	WindowSamples = 200 // approx last 1 min

	GcDelay           = time.Second * 5
	LinkDeadThreshold = time.Second * 30
)
