package state

import (
	"github.com/encodeous/polyamide/conn"
	"math"
	"net/netip"
	"slices"
	"time"
)

type DynamicEndpoint struct {
	node               NodeId
	metric             uint16
	metricRange        uint16
	realLatency        time.Duration
	history            []time.Duration
	windowShiftHistory []bool
	filtered           time.Duration
	lastFilteredValue  time.Duration
	sameSampleCount    int
	lastHeardBack      time.Time
	endpoint           *NetworkEndpoint
}

type NetworkEndpoint struct {
	RemoteInit bool
	WgEndpoint conn.Endpoint
	Ep         netip.AddrPort
}

func (ep *NetworkEndpoint) GetWgEndpoint() conn.Endpoint {
	if ep.WgEndpoint == nil || ep.WgEndpoint.DstToString() != ep.Ep.String() {
		ep.WgEndpoint = &conn.StdNetEndpoint{AddrPort: ep.Ep}
	}
	return ep.WgEndpoint
}

func (n *Neighbour) BestEndpoint() *DynamicEndpoint {
	var best *DynamicEndpoint

	for _, link := range n.Eps {
		if best == nil || link.Metric() < best.Metric() || (link.IsActive() && !best.IsActive()) {
			best = link
		}
	}
	return best
}

func (u *DynamicEndpoint) Node() NodeId {
	return u.node
}

func (u *DynamicEndpoint) IsActive() bool {
	return time.Now().Sub(u.lastHeardBack) <= LinkDeadThreshold
}

func (u *DynamicEndpoint) MetricRange() uint16 {
	return u.metricRange
}

func (u *DynamicEndpoint) Renew() {
	u.lastHeardBack = time.Now()
}

func (u *DynamicEndpoint) IsAlive() bool {
	return u.IsActive() || !u.endpoint.RemoteInit // we never gc endpoints that we have in our config
}

func NewEndpoint(endpoint netip.AddrPort, node NodeId, remoteInit bool, wgEndpoint conn.Endpoint) *DynamicEndpoint {
	return &DynamicEndpoint{
		metric:      INF,
		metricRange: 5000,
		endpoint: &NetworkEndpoint{
			RemoteInit: remoteInit,
			WgEndpoint: wgEndpoint,
			Ep:         endpoint,
		},
		node:     node,
		filtered: time.Millisecond * 1000, // start with a relatively high latency so we don't disrupt existing connections before we are sure
	}
}

func (u *DynamicEndpoint) NetworkEndpoint() *NetworkEndpoint {
	return u.endpoint
}

func (u *DynamicEndpoint) computeRange() time.Duration {
	tmp := make([]time.Duration, len(u.history))
	copy(tmp, u.history)
	slices.Sort(tmp)
	// median := tmp[len(tmp)/2]
	top := tmp[int(float64(len(tmp))*(1-OutlierPercentage))]
	bottom := tmp[int(float64(len(tmp))*OutlierPercentage)]
	return bottom - top
}

func (u *DynamicEndpoint) computeTopP(p float64) time.Duration {
	tmp := make([]time.Duration, len(u.history))
	copy(tmp, u.history)
	slices.Sort(tmp)
	// median := tmp[len(tmp)/2]
	top := tmp[int(float64(len(tmp))*p)]
	return top
}

func (u *DynamicEndpoint) UpdatePing(ping time.Duration) {
	// TODO: We don't have numbers of actual packets being lost.

	u.realLatency = ping

	// not sure if this is a great algorithm, but it is one...
	// We determine a window based on Range
	// outliers will be dealt separately
	// When the latency gets updated, the box will be moved up or down so that it fits the new datapoint.
	// We will use the median of the box as the latency

	// tldr; if the ping fluctuates within +/- 1.5*Range, we don't change it. note, if the ping is very stable, Range will decrease too!

	u.history = append(u.history, u.realLatency)
	u.windowShiftHistory = append(u.windowShiftHistory, false)
	if len(u.history) > WindowSamples {
		u.history = u.history[1:] // discard
	}
	metRan := time.Millisecond * 5000 // default
	if len(u.history) > MinimumConfidenceWindow {
		metRan = u.computeRange()
		//metRan = u.computeTopP(0.95) - u.computeTopP(0.0)
	}

	metRanAdj := float64(metRan) * 2

	// check if ping is within box
	relPosition := 0.7
	below := time.Duration(metRanAdj * relPosition)
	above := time.Duration(metRanAdj * (1 - relPosition))
	shifted := false
	if u.filtered+above < ping {
		// box is too low
		u.filtered = ping - above
		shifted = true
	} else if u.filtered-below > ping {
		// box is too high
		u.filtered = ping + below
		shifted = true
	}
	if shifted {
		u.windowShiftHistory[len(u.windowShiftHistory)-1] = true
	}

	instability := 0
	for _, h := range u.windowShiftHistory {
		if h {
			instability++
		}
	}

	stale := u.sameSampleCount > int(time.Minute*5/ProbeDelay)
	smallChange := math.Abs(float64(u.lastFilteredValue-u.filtered))/float64(u.lastFilteredValue) < MinChangePercent
	if u.lastFilteredValue != 0 && !stale && (smallChange || instability > 10) {
		u.sameSampleCount++
	} else {
		u.sameSampleCount = 0
		u.lastFilteredValue = u.filtered
	}

	// latency in increments of 100 microseconds
	latencyContrib := u.lastFilteredValue.Microseconds() / 100

	u.metric = uint16(min(max(latencyContrib, 1), int64(INF-1)))
	u.metric = uint16(min(max(int64(u.metric), 1), int64(INF-1)))

	u.metricRange = uint16(min(max(metRan.Microseconds()/100, 1), int64(INF-1)))

	//slog.Info("lu", "r", u.realLatency, "f", time.Duration(filtered))
}

func (u *DynamicEndpoint) Metric() uint16 {
	// if link is dead, return INF
	if !u.IsActive() {
		return INF
	}
	return u.metric
}

func (u *DynamicEndpoint) IsRemote() bool {
	return u.endpoint.RemoteInit
}
