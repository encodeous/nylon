package state

import "time"

const (
	INF = ^(uint32)(0)
	// INFM is the maximum value for a metric that is not a retraction.
	INFM = INF - 1
)

var (
	HopCost               = (uint32)(5)          // add a 5 microsecond hop cost to prevent loops on ultra-fast networks.
	LargeChangeThreshold  = (uint32)(100 * 1000) // 100 milliseconds change
	SeqnoRequestHopCount  = (uint8)(64)
	RouteUpdateDelay      = time.Second * 5
	ProbeDelay            = time.Millisecond * 1000
	ProbeRecoveryDelay    = time.Millisecond * 1500
	ProbeDiscoveryDelay   = time.Second * 10
	StarvationDelay       = time.Millisecond * 100
	SeqnoDedupTTL         = time.Second * 3
	NeighbourIOFlushDelay = time.Millisecond * 500
	SafeMTU               = 1200

	// WindowSamples is the sliding window size
	WindowSamples     = int((time.Second * 60) / ProbeDelay)
	OutlierPercentage = 0.05
	// minimum number of samples before we lower the ping
	MinimumConfidenceWindow = int(time.Second * 15 / ProbeDelay)

	GcDelay           = time.Millisecond * 1000
	LinkDeadThreshold = 5 * ProbeDelay
	RouteExpiryTime   = 5 * RouteUpdateDelay

	// client configuration
	ClientKeepaliveInterval = 3 * ProbeDelay
	ClientDeadThreshold     = 2 * ClientKeepaliveInterval

	// central updates
	CentralUpdateDelay = time.Second * 10

	// healthcheck defaults
	HealthCheckDelay       = time.Second * 15
	HealthCheckMaxFailures = 3

	// default port
	DefaultPort = 57175

	// refresh dns
	DnsRefreshDelay = time.Minute * 1
)
