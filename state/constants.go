package state

import "time"

const (
	INF                            = (uint16)(65535)
	HopCost                        = (uint16)(5) // add a 500 microsecond hop cost to prevent loops on ultra-fast networks.
	ProbeCtlDelay                  = time.Second * 5
	RouteUpdateDelay               = time.Second * 5
	ProbeNewDpDelay                = time.Second * 3
	ProbeDpDelay                   = time.Millisecond * 500
	ProbeDpInactiveDelay           = time.Millisecond * 1500
	LinkSwitchMetricCostMultiplier = 1.10
	StarvationDelay                = time.Millisecond * 100
	SeqnoDedupTTL                  = time.Second * 3
	EndpointTTL                    = time.Minute * 5

	// WindowSamples is the sliding window size
	WindowSamples     = int(time.Second * 60 * 5 / ProbeDpDelay) // approx last 5 min
	OutlierPercentage = 0.95
	// minimum number of samples before we lower the ping
	MinimumConfidenceWindow = int(time.Second * 15 / ProbeDpDelay)

	GcDelay           = time.Second * 1
	OtelDelay         = time.Second * 1
	LinkDeadThreshold = 5 * ProbeDpDelay
)
