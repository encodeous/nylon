package state

import "time"

const (
	INF                            = (uint16)(65535)
	HopCost                        = (uint16)(5) // add a 500 microsecond hop cost to prevent loops on ultra-fast networks.
	RouteUpdateDelay               = time.Second * 5
	ProbeDelay                     = time.Millisecond * 500
	DiscoveryDelay                 = time.Millisecond * 1500
	LinkSwitchMetricCostMultiplier = 1.10
	StarvationDelay                = time.Millisecond * 100
	SeqnoDedupTTL                  = time.Second * 3

	// WindowSamples is the sliding window size
	WindowSamples     = int(time.Second * 60 * 5 / ProbeDelay) // approx last 5 min
	OutlierPercentage = 0.95
	// minimum number of samples before we lower the ping
	MinimumConfidenceWindow = int(time.Second * 15 / ProbeDelay)

	GcDelay           = time.Millisecond * 1000
	OtelDelay         = time.Second * 1
	LinkDeadThreshold = 5 * ProbeDelay

	// client configuration
	ClientKeepaliveInterval = 5 * time.Second
	ClientDeadThreshold     = 3 * ClientKeepaliveInterval
)
