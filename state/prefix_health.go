package state

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/digineo/go-ping"
)

// PrefixHealthConfig is immutable configuration stored in CentralCfg.
// Runtime workers and their metrics are owned separately by PrefixHealthMonitor.
type PrefixHealthConfig interface {
	GetPrefix() netip.Prefix
	sameConfig(other PrefixHealthConfig, tunables *RouterTunables) bool
	newMonitor(log *slog.Logger, tunables *RouterTunables, resolver *DNSResolver) PrefixHealthMonitor
}

type PrefixHealthMonitor interface {
	GetMetric() uint32
	Stop()
}

// StaticPrefixHealth represents a static prefix configuration, always advertised with the same metric
type StaticPrefixHealth struct {
	Prefix netip.Prefix `yaml:"prefix"`
	Metric uint32       `yaml:"metric,omitempty"` // the metric to advertise for this prefix
}

func (s *StaticPrefixHealth) GetPrefix() netip.Prefix {
	return s.Prefix
}

func (s *StaticPrefixHealth) sameConfig(other PrefixHealthConfig, _ *RouterTunables) bool {
	o, ok := other.(*StaticPrefixHealth)
	return ok && s.Prefix == o.Prefix && s.Metric == o.Metric
}

func (s *StaticPrefixHealth) newMonitor(_ *slog.Logger, _ *RouterTunables, _ *DNSResolver) PrefixHealthMonitor {
	return staticPrefixHealthMonitor{metric: s.Metric}
}

type staticPrefixHealthMonitor struct {
	metric uint32
}

func (s staticPrefixHealthMonitor) GetMetric() uint32 {
	return s.metric
}

func (staticPrefixHealthMonitor) Stop() {}

type PingPrefixHealth struct {
	Prefix      netip.Prefix   `yaml:"prefix"`
	Addr        netip.Addr     `yaml:"addr"`                   // the address to ping
	MaxFailures *int           `yaml:"max_failures,omitempty"` // number of failures before returning infinite metric
	Delay       *time.Duration `yaml:"delay,omitempty"`        // delay between pings
	BindIf      string         `yaml:"bind_if,omitempty"`      // local interface to bind to
	Metric      *uint32        `yaml:"metric,omitempty"`       // metric override
}

func GetIfIP(itf string, is6 bool) (string, error) {
	ifp, err := net.InterfaceByName(itf)
	if err != nil {
		return "", err
	}

	addrs, err := ifp.Addrs()
	if err != nil {
		return "", err
	}

	for _, address := range addrs {
		addr := netip.MustParsePrefix(address.String()).Addr()
		if addr.Is6() && is6 {
			return addr.String(), nil
		}
		if addr.Is4() && !is6 {
			return addr.String(), nil
		}
	}
	return "", fmt.Errorf("no address found for interface %s", itf)
}

func (p *PingPrefixHealth) GetPrefix() netip.Prefix {
	return p.Prefix
}

func (p *PingPrefixHealth) sameConfig(other PrefixHealthConfig, tunables *RouterTunables) bool {
	o, ok := other.(*PingPrefixHealth)
	return ok &&
		p.Prefix == o.Prefix &&
		p.Addr == o.Addr &&
		p.BindIf == o.BindIf &&
		sameOptionalUint32(p.Metric, o.Metric) &&
		prefixHealthDelay(p.Delay, tunables) == prefixHealthDelay(o.Delay, tunables) &&
		prefixHealthMaxFailures(p.MaxFailures, tunables) == prefixHealthMaxFailures(o.MaxFailures, tunables)
}

func (p *PingPrefixHealth) newMonitor(log *slog.Logger, tunables *RouterTunables, _ *DNSResolver) PrefixHealthMonitor {
	monitor := &pingPrefixHealthMonitor{
		log:         log,
		prefix:      p.Prefix,
		addr:        p.Addr,
		bindIf:      p.BindIf,
		delay:       prefixHealthDelay(p.Delay, tunables),
		maxFailures: prefixHealthMaxFailures(p.MaxFailures, tunables),
		stop:        make(chan struct{}),
	}
	if p.Metric != nil {
		monitor.metricOverride = *p.Metric
		monitor.hasMetricOverride = true
	}
	monitor.lastMetric.Store(INF)
	go monitor.run()
	return monitor
}

type pingPrefixHealthMonitor struct {
	log               *slog.Logger
	prefix            netip.Prefix
	addr              netip.Addr
	bindIf            string
	delay             time.Duration
	maxFailures       int
	metricOverride    uint32
	hasMetricOverride bool
	lastMetric        atomic.Uint32
	stop              chan struct{}
	stopOnce          sync.Once
}

func (p *pingPrefixHealthMonitor) GetMetric() uint32 {
	if p.hasMetricOverride {
		return p.metricOverride
	}
	return p.lastMetric.Load()
}

func (p *pingPrefixHealthMonitor) Stop() {
	p.stopOnce.Do(func() {
		close(p.stop)
	})
}

func (p *pingPrefixHealthMonitor) run() {
	ticker := time.NewTicker(p.delay)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
		}

		bind4 := ""
		bind6 := ""
		var err error
		if p.addr.Is6() {
			if p.bindIf != "" {
				bind6, err = GetIfIP(p.bindIf, true)
			} else {
				bind6 = "::"
			}
		} else {
			if p.bindIf != "" {
				bind4, err = GetIfIP(p.bindIf, false)
			} else {
				bind4 = "0.0.0.0"
			}
		}
		if err != nil {
			p.log.Error("failed to get bind address", "error", err)
			continue
		}
		pinger, err := ping.New(bind4, bind6)
		if err != nil {
			p.log.Error("failed to start pinger", "error", err)
			continue
		}
		rtt, err := pinger.PingAttempts(
			&net.IPAddr{IP: net.IP(p.addr.AsSlice())},
			time.Duration(int64(p.delay)/int64(p.maxFailures)),
			p.maxFailures,
		)
		pinger.Close()
		if err != nil {
			p.lastMetric.Store(INF)
			p.log.Debug("prefix healthcheck failed", "prefix", p.prefix.String(), "addr", p.addr.String(), "error", err)
			continue
		}
		p.lastMetric.Store(DurationToMetric(rtt))
	}
}

type HTTPPrefixHealth struct {
	Prefix netip.Prefix   `yaml:"prefix"`
	URL    string         `yaml:"url"`              // the URL to check
	Delay  *time.Duration `yaml:"delay,omitempty"`  // delay between probes
	Metric *uint32        `yaml:"metric,omitempty"` // metric override
}

func (h *HTTPPrefixHealth) GetPrefix() netip.Prefix {
	return h.Prefix
}

func checkHTTPPrefix(log *slog.Logger, client *http.Client, prefix netip.Prefix, url string) uint32 {
	startTime := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		log.Debug("prefix healthcheck failed", "prefix", prefix.String(), "url", url, "error", err)
		return INF
	}
	_, drainErr := io.Copy(io.Discard, resp.Body)
	closeErr := resp.Body.Close()
	if drainErr != nil {
		log.Debug("failed to drain prefix healthcheck response", "prefix", prefix.String(), "url", url, "error", drainErr)
	}
	if closeErr != nil {
		log.Debug("failed to close prefix healthcheck response", "prefix", prefix.String(), "url", url, "error", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		log.Debug("prefix healthcheck failed", "prefix", prefix.String(), "url", url, "status", resp.StatusCode)
		return INF
	}
	return DurationToMetric(time.Since(startTime))
}

func (h *HTTPPrefixHealth) sameConfig(other PrefixHealthConfig, tunables *RouterTunables) bool {
	o, ok := other.(*HTTPPrefixHealth)
	return ok &&
		h.Prefix == o.Prefix &&
		h.URL == o.URL &&
		sameOptionalUint32(h.Metric, o.Metric) &&
		prefixHealthDelay(h.Delay, tunables) == prefixHealthDelay(o.Delay, tunables)
}

func (h *HTTPPrefixHealth) newMonitor(log *slog.Logger, tunables *RouterTunables, resolver *DNSResolver) PrefixHealthMonitor {
	if resolver == nil {
		resolver = NewDNSResolver(nil)
	}
	delay := prefixHealthDelay(h.Delay, tunables)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = resolver.DialContext
	monitor := &httpPrefixHealthMonitor{
		log:    log,
		prefix: h.Prefix,
		url:    h.URL,
		delay:  delay,
		client: &http.Client{
			Timeout:   delay,
			Transport: transport,
		},
		stop: make(chan struct{}),
	}
	if h.Metric != nil {
		monitor.metricOverride = *h.Metric
		monitor.hasMetricOverride = true
	}
	monitor.lastMetric.Store(INF)
	go monitor.run()
	return monitor
}

type httpPrefixHealthMonitor struct {
	log               *slog.Logger
	prefix            netip.Prefix
	url               string
	delay             time.Duration
	client            *http.Client
	metricOverride    uint32
	hasMetricOverride bool
	lastMetric        atomic.Uint32
	stop              chan struct{}
	stopOnce          sync.Once
}

func (h *httpPrefixHealthMonitor) GetMetric() uint32 {
	metric := h.lastMetric.Load()
	if metric == INF {
		return INF
	}
	if h.hasMetricOverride {
		return h.metricOverride
	}
	return metric
}

func (h *httpPrefixHealthMonitor) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
}

func (h *httpPrefixHealthMonitor) run() {
	ticker := time.NewTicker(h.delay)
	defer ticker.Stop()
	defer h.client.CloseIdleConnections()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.lastMetric.Store(checkHTTPPrefix(h.log, h.client, h.prefix, h.url))
		}
	}
}

type PrefixHealthWrapper struct {
	PrefixHealth PrefixHealthConfig
}

func (p PrefixHealthWrapper) GetPrefix() netip.Prefix {
	return p.PrefixHealth.GetPrefix()
}

// SameConfig reports whether two prefix health checks have equivalent configuration.
func (p PrefixHealthWrapper) SameConfig(other PrefixHealthWrapper, tunables *RouterTunables) bool {
	if p.PrefixHealth == nil || other.PrefixHealth == nil {
		return p.PrefixHealth == other.PrefixHealth
	}
	return p.PrefixHealth.sameConfig(other.PrefixHealth, tunables)
}

func (p PrefixHealthWrapper) NewMonitor(log *slog.Logger, tunables *RouterTunables, resolver *DNSResolver) PrefixHealthMonitor {
	return p.PrefixHealth.newMonitor(log, tunables, resolver)
}

func (p PrefixHealthWrapper) StaticMetric() (uint32, bool) {
	config, ok := p.PrefixHealth.(*StaticPrefixHealth)
	if !ok {
		return 0, false
	}
	return config.Metric, true
}

func sameOptionalUint32(a, b *uint32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func prefixHealthDelay(value *time.Duration, tunables *RouterTunables) time.Duration {
	if value != nil {
		return *value
	}
	return tunables.HealthCheckDelay
}

func prefixHealthMaxFailures(value *int, tunables *RouterTunables) int {
	if value != nil {
		return *value
	}
	return tunables.HealthCheckMaxFailures
}

func (p PrefixHealthWrapper) MarshalYAML() (interface{}, error) {
	switch v := p.PrefixHealth.(type) {
	case *StaticPrefixHealth:
		return struct {
			Type                string `yaml:"type"`
			*StaticPrefixHealth `yaml:",inline"`
		}{
			Type:               "static",
			StaticPrefixHealth: v,
		}, nil
	case *PingPrefixHealth:
		return struct {
			Type              string `yaml:"type"`
			*PingPrefixHealth `yaml:",inline"`
		}{
			Type:             "ping",
			PingPrefixHealth: v,
		}, nil
	case *HTTPPrefixHealth:
		return struct {
			Type              string `yaml:"type"`
			*HTTPPrefixHealth `yaml:",inline"`
		}{
			Type:             "http",
			HTTPPrefixHealth: v,
		}, nil
	default:
		return nil, nil
	}
}

func (p *PrefixHealthWrapper) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw struct {
		Type string `yaml:"type"`
	}
	if err := unmarshal(&raw); err != nil {
		return err
	}

	switch raw.Type {
	case "static":
		var sp StaticPrefixHealth
		if err := unmarshal(&sp); err != nil {
			return err
		}
		p.PrefixHealth = &sp
	case "ping":
		var pp PingPrefixHealth
		if err := unmarshal(&pp); err != nil {
			return err
		}
		p.PrefixHealth = &pp
	case "http":
		var hp HTTPPrefixHealth
		if err := unmarshal(&hp); err != nil {
			return err
		}
		p.PrefixHealth = &hp
	default:
		return fmt.Errorf("unknown prefix health type: %s", raw.Type)
	}
	return nil
}
