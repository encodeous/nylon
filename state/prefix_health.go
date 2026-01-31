package state

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/digineo/go-ping"
)

type PrefixHealth interface {
	GetMetric() uint32 // Metric does not block, and returns the advertised metric for this prefix
	GetPrefix() netip.Prefix
	Start(log *slog.Logger) // Start begins any background monitoring required for this prefix
	Stop()
}

// StaticPrefixHealth represents a static prefix configuration, always advertised with the same metric
type StaticPrefixHealth struct {
	Prefix netip.Prefix `yaml:"prefix"`
	Metric uint32       `yaml:"metric,omitempty"` // the metric to advertise for this prefix
}

func (s *StaticPrefixHealth) Stop() {
	// do nothing
}

func (s *StaticPrefixHealth) GetMetric() uint32 {
	return s.Metric
}
func (s *StaticPrefixHealth) GetPrefix() netip.Prefix {
	return s.Prefix
}
func (s *StaticPrefixHealth) Start(log *slog.Logger) {
	// do nothing
}

type PingPrefixHealth struct {
	Prefix      netip.Prefix   `yaml:"prefix"`
	Addr        netip.Addr     `yaml:"addr"`                   // the address to ping
	MaxFailures *int           `yaml:"max_failures,omitempty"` // number of failures before returning infinite metric
	Delay       *time.Duration `yaml:"delay,omitempty"`        // delay between pings
	BindIf      string         `yaml:"bind_if,omitempty"`      // local interface to bind to
	Metric      *uint32        `yaml:"metric,omitempty"`       // metric override
	lastMetric  uint32
	running     atomic.Bool
}

func (p *PingPrefixHealth) Stop() {
	p.running.Swap(false)
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

func (p *PingPrefixHealth) GetMetric() uint32 {
	if p.Metric != nil {
		return *p.Metric
	}
	return p.lastMetric
}
func (p *PingPrefixHealth) GetPrefix() netip.Prefix {
	return p.Prefix
}
func (p *PingPrefixHealth) Start(log *slog.Logger) {
	p.running.Swap(true)
	if p.Delay == nil {
		p.Delay = &HealthCheckDelay
	}
	if p.MaxFailures == nil {
		p.MaxFailures = &HealthCheckMaxFailures
	}
	go func() {
		ticker := time.NewTicker(*p.Delay)
		for p.running.Load() {
			time.Sleep(*p.Delay)
			p.lastMetric = INF
			bind4 := ""
			bind6 := ""
			var err error
			if p.Addr.Is6() {
				if p.BindIf != "" {
					bind6, err = GetIfIP(p.BindIf, true)
				} else {
					bind6 = "::"
				}
			} else {
				if p.BindIf != "" {
					bind4, err = GetIfIP(p.BindIf, false)
				} else {
					bind4 = "0.0.0.0"
				}
			}
			if err != nil {
				log.Error("failed to get bind address", "error", err)
				continue
			}
			pinger, err := ping.New(bind4, bind6)
			if err != nil {
				log.Error("failed to start pinger", "error", err)
				continue
			}
			for p.running.Load() { // TODO: add a way to interrupt this sleep, if ticker has a high delay
				<-ticker.C
				// ICMP ping
				addr := &net.IPAddr{IP: net.IP(p.Addr.AsSlice())}
				rtt, err := pinger.PingAttempts(addr, time.Duration(int64(*p.Delay)/int64(*p.MaxFailures)), *p.MaxFailures)
				if err != nil {
					// failed
					p.lastMetric = INF
					log.Debug("prefix healthcheck failed", "prefix", p.Prefix.String(), "addr", p.Addr.String(), "error", err)
					pinger.Close()
					break // break to outer loop to recreate pinger
				} else {
					// success
					p.lastMetric = DurationToMetric(rtt)
				}
			}
		}
	}()
}

type HTTPPrefixHealth struct {
	Prefix     netip.Prefix   `yaml:"prefix"`
	URL        string         `yaml:"url"`              // the URL to check
	Delay      *time.Duration `yaml:"delay,omitempty"`  // delay between probes
	Metric     *uint32        `yaml:"metric,omitempty"` // metric override
	lastMetric uint32
	running    atomic.Bool
}

func (h *HTTPPrefixHealth) Stop() {
	h.running.Swap(false)
}

func (h *HTTPPrefixHealth) GetMetric() uint32 {
	if h.Metric != nil {
		return *h.Metric
	}
	return h.lastMetric
}
func (h *HTTPPrefixHealth) GetPrefix() netip.Prefix {
	return h.Prefix
}
func (h *HTTPPrefixHealth) Start(log *slog.Logger) {
	h.lastMetric = INF
	h.running.Swap(true)
	if h.Delay == nil {
		h.Delay = &HealthCheckDelay
	}
	go func() {
		ticker := time.NewTicker(*h.Delay)
		defer ticker.Stop()
		for h.running.Load() { // TODO: add a way to interrupt this sleep, if ticker has a high delay
			<-ticker.C
			// HTTP probe logic would go here
			startTime := time.Now()
			resp, err := http.Get(h.URL)
			if err != nil || resp.StatusCode != http.StatusOK {
				// failed
				h.lastMetric = INF
				log.Debug("prefix healthcheck failed", "prefix", h.Prefix.String(), "url", h.URL, "error", err)
			} else {
				// success
				rtt := time.Since(startTime)
				h.lastMetric = DurationToMetric(rtt)
			}
		}
	}()
}

type PrefixHealthWrapper struct {
	PrefixHealth
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
		return nil
	}
	return nil
}
