package impl

import "time"

const (
	INF                            = (uint16)(65535)
	ProbeCtlDelay                  = time.Second * 5
	RouteUpdateDelay               = time.Second * 5
	ProbeNewDpDelay                = time.Second * 3
	ProbeDpDelay                   = time.Millisecond * 400
	LinkSwitchMetricCostMultiplier = 1.3
	StarvationDelay                = time.Millisecond * 100
	SeqnoDedupTTL                  = time.Second * 3
	EndpointTTL                    = time.Minute * 5

	// WindowSamples is the sliding window size
	WindowSamples     = int(time.Second * 60 * 5 / ProbeDpDelay) // approx last 5 min
	OutlierPercentage = 0.95
	// minimum number of samples before we lower the ping
	MinimumConfidenceWindow = int(time.Second * 15 / ProbeDpDelay)

	GcDelay           = time.Second * 1
	LinkDeadThreshold = 5 * ProbeDpDelay
)
