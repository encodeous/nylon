package state

import "time"

const (
	INF = (uint16)(65535)
)

var (
	HopCost                        = (uint16)(5) // add a 500 microsecond hop cost to prevent loops on ultra-fast networks.
	RouteUpdateDelay               = time.Second * 5
	ProbeDelay                     = time.Millisecond * 1000
	ProbeRecoveryDelay             = time.Millisecond * 1500
	ProbeDiscoveryDelay            = time.Second * 10
	LinkSwitchMetricCostMultiplier = 1.10
	StarvationDelay                = time.Millisecond * 100
	SeqnoDedupTTL                  = time.Second * 3
	NeighbourIOFlushDelay          = time.Millisecond * 500
	SafeMTU                        = 1200

	// WindowSamples is the sliding window size
	WindowSamples     = int((time.Second * 60) / ProbeDelay)
	OutlierPercentage = 0.05
	// minimum number of samples before we lower the ping
	MinimumConfidenceWindow = int(time.Second * 15 / ProbeDelay)

	GcDelay           = time.Millisecond * 1000
	LinkDeadThreshold = 5 * ProbeDelay

	// client configuration
	ClientKeepaliveInterval = 25 * time.Second
	ClientDeadThreshold     = 3 * ClientKeepaliveInterval

	// central updates
	CentralUpdateDelay = time.Second * 10
)
