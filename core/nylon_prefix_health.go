package core

import (
	"net/netip"
	"time"

	"github.com/encodeous/nylon/state"
)

// advertisedPrefixHealth binds immutable prefix configuration to the live
// monitor used for a locally advertised prefix.
type advertisedPrefixHealth struct {
	config  state.PrefixHealthWrapper
	monitor state.PrefixHealthMonitor
}

func (n *Nylon) reconcileAdvertisedPrefixes(next *state.CentralCfg) {
	if n.prefixHealth == nil {
		n.prefixHealth = make(map[netip.Prefix]advertisedPrefixHealth)
	}
	nextNode := next.TryGetNode(n.LocalCfg.Id)
	if nextNode == nil {
		return
	}

	desiredLocal := make(map[netip.Prefix]int)
	for i, prefix := range nextNode.Prefixes {
		desiredLocal[prefix.GetPrefix()] = i
	}

	for prefix, adv := range n.RouterState.Advertised {
		if adv.NodeId != n.LocalCfg.Id {
			continue
		}
		if _, ok := desiredLocal[prefix]; !ok {
			if old, ok := n.prefixHealth[prefix]; ok {
				old.monitor.Stop()
				delete(n.prefixHealth, prefix)
			}
			delete(n.RouterState.Advertised, prefix)
		}
	}

	for prefix, index := range desiredLocal {
		config := nextNode.Prefixes[index]
		var health advertisedPrefixHealth
		if current, ok := n.prefixHealth[prefix]; ok && current.config.SameConfig(config, &n.RouterTunables) {
			health = current
		} else {
			if current, ok := n.prefixHealth[prefix]; ok {
				current.monitor.Stop()
			}
			n.Log.Debug("starting prefix healthcheck", "prefix", prefix)
			health = advertisedPrefixHealth{
				config:  config,
				monitor: config.NewMonitor(n.Log, &n.RouterTunables, n.DNSResolver),
			}
			n.prefixHealth[prefix] = health
		}
		n.RouterState.Advertised[prefix] = state.Advertisement{
			NodeId:   n.LocalCfg.Id,
			Expiry:   maxConfigTime,
			MetricFn: health.monitor.GetMetric,
			ExpiryFn: func() {
				health.monitor.Stop()
			},
		}
	}
}

var maxConfigTime = time.Unix(1<<63-62135596801, 999999999)
