package state

import "time"

// RouterTunables contains all timing and algorithm parameters for the router.
// These are set once at startup and should not be mutated after the Nylon instance starts.
type RouterTunables struct {
	HopCost               uint32 // add a 5 microsecond hop cost to prevent loops on ultra-fast networks.
	LargeChangeThreshold  uint32 // 100 milliseconds change
	SeqnoRequestHopCount  uint8
	RouteUpdateDelay      time.Duration
	ProbeDelay            time.Duration
	ProbeRecoveryDelay    time.Duration
	ProbeDiscoveryDelay   time.Duration
	StarvationDelay       time.Duration
	SeqnoDedupTTL         time.Duration
	NeighbourIOFlushDelay time.Duration
	SafeMTU               int

	// WindowSamples is the sliding window size
	WindowSamples     int
	OutlierPercentage float64
	// MinimumConfidenceWindow is the minimum number of samples before we lower the ping
	MinimumConfidenceWindow int

	GcDelay           time.Duration
	LinkDeadThreshold time.Duration
	RouteExpiryTime   time.Duration

	// client configuration
	ClientKeepaliveInterval time.Duration
	ClientDeadThreshold     time.Duration

	// central updates
	CentralUpdateDelay time.Duration

	// healthcheck defaults
	HealthCheckDelay       time.Duration
	HealthCheckMaxFailures int

	EndpointResolveExpiry time.Duration
	EndpointResolveDelay  time.Duration

	MaxConfigSize int64
}

// NylonOptions contains runtime flags set at startup (typically from CLI flags).
type NylonOptions struct {
	DBG_log_probe        bool
	DBG_log_wireguard    bool
	DBG_log_repo_updates bool
	DBG_log_json         bool
	DBG_debug            bool
	DBG_trace            bool
	DBG_trace_tc         bool
}

func DefaultRouterTunables() RouterTunables {
	probeDelay := time.Millisecond * 1000
	routeUpdateDelay := time.Second * 5
	return RouterTunables{
		HopCost:               5,
		LargeChangeThreshold:  100 * 1000,
		SeqnoRequestHopCount:  64,
		RouteUpdateDelay:      routeUpdateDelay,
		ProbeDelay:            probeDelay,
		ProbeRecoveryDelay:    time.Millisecond * 1500,
		ProbeDiscoveryDelay:   time.Second * 10,
		StarvationDelay:       time.Millisecond * 100,
		SeqnoDedupTTL:         time.Second * 3,
		NeighbourIOFlushDelay: time.Millisecond * 500,
		SafeMTU:               1200,

		WindowSamples:           int((time.Second * 60) / probeDelay),
		OutlierPercentage:       0.05,
		MinimumConfidenceWindow: int(time.Second * 15 / probeDelay),

		GcDelay:           time.Millisecond * 1000,
		LinkDeadThreshold: 5 * probeDelay,
		RouteExpiryTime:   5 * routeUpdateDelay,

		ClientKeepaliveInterval: 3 * probeDelay,
		ClientDeadThreshold:     6 * probeDelay, // 2 * ClientKeepaliveInterval

		CentralUpdateDelay: time.Second * 10,

		HealthCheckDelay:       time.Second * 15,
		HealthCheckMaxFailures: 3,

		EndpointResolveExpiry: time.Minute * 1,
		EndpointResolveDelay:  time.Second * 15,

		MaxConfigSize: 1 << 20, // 1 MB
	}
}

func DefaultNylonOptions() NylonOptions {
	return NylonOptions{}
}
