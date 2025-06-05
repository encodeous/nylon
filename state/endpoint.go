package state

import (
	"fmt"
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"math"
	"net/netip"
	"slices"
	"time"
)

type DynamicEndpoint struct {
	node          NodeId
	history       []time.Duration
	histSort      []time.Duration
	dirty         bool
	prevMedian    time.Duration
	lastHeardBack time.Time
	expRTT        float64
	endpoint      *NetworkEndpoint
}

type NetworkEndpoint struct {
	RemoteInit bool
	WgEndpoint conn.Endpoint
	Ep         netip.AddrPort
}

func (ep *NetworkEndpoint) GetWgEndpoint(device *device.Device) conn.Endpoint {
	if ep.WgEndpoint == nil || ep.WgEndpoint.DstToString() != ep.Ep.String() {
		wgEp, err := device.Bind().ParseEndpoint(ep.Ep.String())
		if err != nil {
			panic(fmt.Sprintf("Failed to parse endpoint: %s, %v", ep.Ep.String(), err))
		}
		ep.WgEndpoint = wgEp
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

func (u *DynamicEndpoint) Renew() {
	if !u.IsActive() {
		u.history = u.history[:0]
		u.expRTT = math.Inf(1)
		u.dirty = true
	}
	u.lastHeardBack = time.Now()
}

func (u *DynamicEndpoint) IsAlive() bool {
	return u.IsActive() || !u.endpoint.RemoteInit // we never gc endpoints that we have in our config
}

func NewEndpoint(endpoint netip.AddrPort, node NodeId, remoteInit bool, wgEndpoint conn.Endpoint) *DynamicEndpoint {
	return &DynamicEndpoint{
		endpoint: &NetworkEndpoint{
			RemoteInit: remoteInit,
			WgEndpoint: wgEndpoint,
			Ep:         endpoint,
		},
		history: make([]time.Duration, 0),
		node:    node,
		expRTT:  math.Inf(1),
	}
}

func (u *DynamicEndpoint) NetworkEndpoint() *NetworkEndpoint {
	return u.endpoint
}

func (u *DynamicEndpoint) calcR() (time.Duration, time.Duration, time.Duration) {
	if len(u.history) < MinimumConfidenceWindow {
		return time.Second * 10, time.Second * 10, time.Second * 10
	}
	if u.dirty {
		u.histSort = slices.Clone(u.history)
		slices.Sort(u.histSort)
		u.dirty = false
	}
	le := len(u.histSort)
	low := u.histSort[int(float64(le)*OutlierPercentage)]
	high := u.histSort[int(float64(le)*(1-OutlierPercentage))]
	med := u.histSort[le/2]
	return low, med, high
}

func (u *DynamicEndpoint) LowRange() time.Duration {
	l, _, _ := u.calcR()
	return l
}

func (u *DynamicEndpoint) HighRange() time.Duration {
	_, _, h := u.calcR()
	return h
}

func (u *DynamicEndpoint) FilteredPing() time.Duration {
	return time.Duration(int64(u.expRTT))
}

func (u *DynamicEndpoint) StabilizedPing() time.Duration {
	l, m, h := u.calcR()
	// don't change median unless it is out of the range of l <= h
	if l > u.prevMedian || h < u.prevMedian {
		u.prevMedian = m
	}
	return u.prevMedian
}

func SwitchHeuristic(curRoute *Route, newRoute PubRoute, metric uint16, via *DynamicEndpoint) bool {
	// prevent oscillation
	curMetric := float64(curRoute.PubMetric)
	newMetric := float64(metric)
	if (newMetric+float64(via.StabilizedPing()))*LinkSwitchMetricCostMultiplier > curMetric {
		return false
	}
	return true
}

func (u *DynamicEndpoint) UpdatePing(ping time.Duration) {
	// sometimes our system clock is not fast enough, so ping is 0
	if ping == 0 {
		ping = time.Microsecond * 100
	}

	f := float64(ping)
	alpha := 0.0836
	if u.expRTT == math.Inf(1) {
		u.expRTT = f
	}
	u.expRTT = alpha*f + (1-alpha)*u.expRTT
	u.history = append(u.history, u.FilteredPing())
	if len(u.history) > WindowSamples {
		u.history = u.history[1:]
	}
	u.dirty = true
}

func (u *DynamicEndpoint) Metric() uint16 {
	// if link is dead, return INF
	if !u.IsActive() {
		return INF
	}
	return uint16(min(u.StabilizedPing().Microseconds()/100, int64(INF)-1))
}

func (u *DynamicEndpoint) IsRemote() bool {
	return u.endpoint.RemoteInit
}
