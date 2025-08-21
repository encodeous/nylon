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

type Endpoint interface {
	Node() NodeId
	UpdatePing(ping time.Duration)
	Metric() uint16
	IsRemote() bool
	IsActive() bool
	AsNylonEndpoint() *NylonEndpoint
}

type NylonEndpoint struct {
	node          NodeId
	history       []time.Duration
	histSort      []time.Duration
	dirty         bool
	prevMedian    time.Duration
	lastHeardBack time.Time
	expRTT        float64
	remoteInit    bool
	WgEndpoint    conn.Endpoint
	Ep            netip.AddrPort
}

func (ep *NylonEndpoint) AsNylonEndpoint() *NylonEndpoint {
	return ep
}

func (ep *NylonEndpoint) GetWgEndpoint(device *device.Device) conn.Endpoint {
	if ep.WgEndpoint == nil || ep.WgEndpoint.DstToString() != ep.Ep.String() {
		wgEp, err := device.Bind().ParseEndpoint(ep.Ep.String())
		if err != nil {
			panic(fmt.Sprintf("Failed to parse endpoint: %s, %v", ep.Ep.String(), err))
		}
		ep.WgEndpoint = wgEp
	}
	return ep.WgEndpoint
}

func (n *Neighbour) BestEndpoint() Endpoint {
	var best Endpoint

	for _, link := range n.Eps {
		if best == nil || link.Metric() < best.Metric() || (link.IsActive() && !best.IsActive()) {
			best = link
		}
	}
	return best
}

func (u *NylonEndpoint) Node() NodeId {
	return u.node
}

func (u *NylonEndpoint) IsActive() bool {
	return time.Now().Sub(u.lastHeardBack) <= LinkDeadThreshold
}

func (u *NylonEndpoint) Renew() {
	if !u.IsActive() {
		u.history = u.history[:0]
		u.expRTT = math.Inf(1)
		u.dirty = true
	}
	u.lastHeardBack = time.Now()
}

func (u *NylonEndpoint) IsAlive() bool {
	return u.IsActive() || !u.remoteInit // we never gc endpoints that we have in our config
}

func NewEndpoint(endpoint netip.AddrPort, node NodeId, remoteInit bool, wgEndpoint conn.Endpoint) *NylonEndpoint {
	return &NylonEndpoint{
		remoteInit: remoteInit,
		WgEndpoint: wgEndpoint,
		Ep:         endpoint,
		history:    make([]time.Duration, 0),
		node:       node,
		expRTT:     math.Inf(1),
	}
}

func (u *NylonEndpoint) calcR() (time.Duration, time.Duration, time.Duration) {
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

func (u *NylonEndpoint) LowRange() time.Duration {
	l, _, _ := u.calcR()
	return l
}

func (u *NylonEndpoint) HighRange() time.Duration {
	_, _, h := u.calcR()
	return h
}

func (u *NylonEndpoint) FilteredPing() time.Duration {
	return time.Duration(int64(u.expRTT))
}

func (u *NylonEndpoint) StabilizedPing() time.Duration {
	l, m, h := u.calcR()
	// don't change median unless it is out of the range of l <= h
	if l > u.prevMedian || h < u.prevMedian {
		u.prevMedian = m
	}
	return u.prevMedian
}

func (u *NylonEndpoint) UpdatePing(ping time.Duration) {
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

func (u *NylonEndpoint) Metric() uint16 {
	// if link is dead, return INF
	if !u.IsActive() {
		return INF
	}
	return uint16(min(u.StabilizedPing().Microseconds()/100, int64(INF)-1))
}

func (u *NylonEndpoint) IsRemote() bool {
	return u.remoteInit
}
