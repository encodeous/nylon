package state

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/netip"
	"slices"
	"strconv"
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

/*
		DynamicEndpoint represents either an ip:port or a dns name. This may be resolved to a different address at any time

		Examples:
	    - nylon.example.com -> resolves to <ip>:57175 (DefaultPort)
		- nylon2.example.com:12345 -> resolves to <ip>:12345
		- SRV record: _nylon._udp.example.com. port: 8000 target: nylon3.example.com -> resolves to <ip>:8000
*/
type DynamicEndpoint struct {
	Value      string
	lastValue  netip.AddrPort
	lastUpdate time.Time
	rw         *sync.RWMutex
}

func NewDynamicEndpoint(value string) *DynamicEndpoint {
	return &DynamicEndpoint{
		Value: value,
		rw:    &sync.RWMutex{},
	}
}

func (ep *DynamicEndpoint) Parse() (host string, port uint16, err error) {
	// Try to parse as AddrPort directly first to handle cases like [::1]:port correctly
	if ap, err := netip.ParseAddrPort(ep.Value); err == nil {
		return ap.Addr().String(), ap.Port(), nil
	}

	h, portStr, err := net.SplitHostPort(ep.Value)
	if err != nil {
		// No port specified?
		// TODO: more robust validation
		return ep.Value, uint16(DefaultPort), nil
	} else {
		p, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %w", err)
		}
		return h, uint16(p), nil
	}
}

func (ep *DynamicEndpoint) Refresh() (netip.AddrPort, error) {
	// 1. Try to parse as AddrPort directly
	if ap, err := netip.ParseAddrPort(ep.Value); err == nil {
		return ap, nil
	}

	ep.rw.RLock()
	// if this endpoint is down, we will refresh every EndpointResolveDelay
	if time.Now().Sub(ep.lastUpdate) < EndpointResolveExpiry && ep.lastValue != (netip.AddrPort{}) {
		ep.rw.RUnlock()
		return ep.lastValue, nil
	}
	ep.rw.RUnlock()

	host, port, err := ep.Parse()
	if err != nil {
		return netip.AddrPort{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	// 2. Try SRV lookup
	target, srvPort, err := ResolveSRV(ctx, "nylon", "udp", host)
	if err == nil {
		addrs, err := ResolveName(ctx, target)
		if err == nil && len(addrs) > 0 {
			ep.rw.Lock()
			defer ep.rw.Unlock()
			ep.lastUpdate = time.Now()
			ep.lastValue = netip.AddrPortFrom(addrs[0], srvPort)
			return ep.lastValue, nil
		}
	}

	// 3. Normal A/AAAA lookup
	addrs, err := ResolveName(ctx, host)
	if err != nil {
		return netip.AddrPort{}, err
	}
	if len(addrs) == 0 {
		return netip.AddrPort{}, fmt.Errorf("no addresses found for %s", host)
	}

	ep.rw.Lock()
	defer ep.rw.Unlock()
	ep.lastUpdate = time.Now()
	ep.lastValue = netip.AddrPortFrom(addrs[0], port)
	return ep.lastValue, nil
}

func (ep *DynamicEndpoint) Get() (netip.AddrPort, error) {
	if ap, err := netip.ParseAddrPort(ep.Value); err == nil {
		return ap, nil
	}
	ep.rw.RLock()
	defer ep.rw.RUnlock()
	if ep.lastValue != (netip.AddrPort{}) {
		return ep.lastValue, nil
	}
	return netip.AddrPort{}, fmt.Errorf("endpoint not resolved")
}

func (ep *DynamicEndpoint) Clear() {
	ep.rw.Lock()
	defer ep.rw.Unlock()
	ep.lastUpdate = time.Time{}
}

func (ep *DynamicEndpoint) String() string {
	return ep.Value
}

func (ep *DynamicEndpoint) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		return err
	}
	ep.Value = s
	ep.rw = &sync.RWMutex{}
	return nil
}

func (ep *DynamicEndpoint) MarshalYAML() (interface{}, error) {
	return ep.Value, nil
}

type NylonEndpoint struct {
	history       []time.Duration
	histSort      []time.Duration
	dirty         bool
	prevMedian    time.Duration
	lastHeardBack time.Time
	expRTT        float64
	remoteInit    bool
	WgEndpoint    conn.Endpoint
	DynEP         *DynamicEndpoint
}

func (ep *NylonEndpoint) AsNylonEndpoint() *NylonEndpoint {
	return ep
}

func (ep *NylonEndpoint) GetWgEndpoint(device *device.Device) (conn.Endpoint, error) {
	ap, err := ep.DynEP.Get()
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
		if best == nil || link.Metric() < best.Metric() || (link.IsActive() && !best.IsActive()) {
			best = link
		}
	}
	return best
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

func NewEndpoint(endpoint *DynamicEndpoint, remoteInit bool, wgEndpoint conn.Endpoint) *NylonEndpoint {
	return &NylonEndpoint{
		remoteInit: remoteInit,
		WgEndpoint: wgEndpoint,
		DynEP:      endpoint,
		history:    make([]time.Duration, 0),
		expRTT:     math.Inf(1),
	}
}

func (u *NylonEndpoint) calcR() (time.Duration, time.Duration, time.Duration) {
	if len(u.history) < MinimumConfidenceWindow {
		return time.Second * 1, time.Second * 1, time.Second * 1
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
