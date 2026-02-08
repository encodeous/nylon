package state

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

// SetResolvers configures the global default resolver
func SetResolvers(resolvers []string) {
	if len(resolvers) != 0 {
		net.DefaultResolver = &net.Resolver{
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: time.Second * 10}
				var lastErr error
				for _, r := range resolvers {
					conn, err := d.DialContext(ctx, network, r)
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				return nil, lastErr
			},
		}
	}
}

// ResolveName resolves a hostname to a list of IP addresses
func ResolveName(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var addrs []netip.Addr
	for _, ipStr := range ips {
		if addr, err := netip.ParseAddr(ipStr); err == nil {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}

// ResolveSRV resolves an SRV record using the default resolver
func ResolveSRV(ctx context.Context, service, proto, name string) (string, uint16, error) {
	_, addrs, err := net.DefaultResolver.LookupSRV(ctx, service, proto, name)
	if err != nil {
		return "", 0, err
	}
	if len(addrs) == 0 {
		return "", 0, fmt.Errorf("no SRV records found")
	}
	// Return the first SRV target and port
	return strings.TrimSuffix(addrs[0].Target, "."), addrs[0].Port, nil
}
