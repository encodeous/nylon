package state

import (
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
)

type Endpoint interface {
	UpdatePing(ping time.Duration)
	Metric() uint32
	IsRemote() bool
	IsActive() bool
	AsNylonEndpoint() *NylonEndpoint
}

type NylonEndpoint struct {
	sync.RWMutex  // this mutex is for rtt smoothing and metric calculation
	t             *RouterTunables
	history       []time.Duration
	histSort      []time.Duration
	dirty         bool
	prevMedian    time.Duration
	lastHeardBack time.Time
	expRTT        float64
	remoteInit    bool
	WgEndpoint    conn.Endpoint
	Address       string
}

func (ep *NylonEndpoint) AsNylonEndpoint() *NylonEndpoint {
	return ep
}

func (ep *NylonEndpoint) GetWgEndpoint(device *device.Device, resolver *EndpointResolver) (conn.Endpoint, error) {
	ap, err := resolver.Get(ep.Address)
	if err != nil {
		return nil, err
	}

	if ep.WgEndpoint == nil || ep.WgEndpoint.DstIPPort() != ap {
		wgEp, err := device.Bind().ParseEndpoint(ap.String())
		if err != nil {
			return nil, fmt.Errorf("failed to parse endpoint: %s, %v", ap.String(), err)
		}
		ep.WgEndpoint = wgEp
	}
	return ep.WgEndpoint, nil
}

func (n *Neighbour) BestEndpoint() Endpoint {
	var best Endpoint

	for _, link := range n.Eps {
		if !link.IsActive() {
			continue
		}
		if best == nil || link.Metric() < best.Metric() {
			best = link
		}
	}
	return best
}

func (u *NylonEndpoint) isActiveUnlocked() bool {
	return time.Since(u.lastHeardBack) <= u.t.LinkDeadThreshold
}

func (u *NylonEndpoint) IsActive() bool {
	u.RLock()
	defer u.RUnlock()
	return u.isActiveUnlocked()
}

func (u *NylonEndpoint) Renew() {
	u.Lock()
	defer u.Unlock()
	if !u.isActiveUnlocked() {
		u.history = u.history[:0]
		u.expRTT = math.Inf(1)
		u.dirty = true
	}
	u.lastHeardBack = time.Now()
}

func (u *NylonEndpoint) IsAlive() bool {
	return u.IsActive() || !u.remoteInit // we never gc endpoints that we have in our config
}

func NewEndpoint(address string, remoteInit bool, wgEndpoint conn.Endpoint, t *RouterTunables) *NylonEndpoint {
	return &NylonEndpoint{
		t:          t,
		remoteInit: remoteInit,
		WgEndpoint: wgEndpoint,
		Address:    address,
		history:    make([]time.Duration, 0),
		expRTT:     math.Inf(1),
	}
}

func (u *NylonEndpoint) calcR() (time.Duration, time.Duration, time.Duration) {
	u.Lock()
	defer u.Unlock()
	if len(u.history) < u.t.MinimumConfidenceWindow {
		return time.Second * 1, time.Second * 1, time.Second * 1
	}
	if u.dirty {
		u.histSort = slices.Clone(u.history)
		slices.Sort(u.histSort)
		u.dirty = false
	}
	le := len(u.histSort)
	low := u.histSort[int(float64(le)*u.t.OutlierPercentage)]
	high := u.histSort[int(float64(le)*(1-u.t.OutlierPercentage))]
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
	u.Lock()
	defer u.Unlock()
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
	u.history = append(u.history, time.Duration(int64(u.expRTT)))
	if len(u.history) > u.t.WindowSamples {
		u.history = u.history[1:]
	}
	u.dirty = true
}

func (u *NylonEndpoint) Metric() uint32 {
	// if link is dead, return INF
	if !u.IsActive() {
		return INF
	}
	return DurationToMetric(u.StabilizedPing())
}

func (u *NylonEndpoint) IsRemote() bool {
	return u.remoteInit
}

func DurationToMetric(d time.Duration) uint32 {
	if d == time.Duration(math.MaxInt64) {
		return INF
	}
	return uint32(min(d.Microseconds(), int64(INF)-1))
}

func MetricToDuration(m uint32) time.Duration {
	if m >= INF {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(m) * time.Microsecond
}
