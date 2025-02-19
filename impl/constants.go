package impl

import "time"

const (
	INF                            = (uint16)(65535)
	ProbeCtlDelay                  = time.Second * 5
	RouteUpdateDelay               = time.Second * 5
	ProbeDpDelay                   = time.Millisecond * 400
	LinkSwitchMetricCostMultiplier = 1.1
	StarvationDelay                = time.Millisecond * 100
	SeqnoDedupTTL                  = time.Second * 3
	EndpointTTL                    = time.Minute * 5

	// WindowSamples is the sliding window size
	WindowSamples = int(time.Second * 60 / ProbeDpDelay) // approx last 1 min
	// minimum number of samples before we lower the ping
	MinimumConfidenceWindow = int(time.Second * 5 / ProbeDpDelay)

	GcDelay           = time.Second * 1
	LinkDeadThreshold = 5 * ProbeDpDelay
)
