package state

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// DNSResolver performs Nylon's DNS lookups without changing net.DefaultResolver.
// A Nylon instance can therefore use its configured DNS servers independently
// from other instances in the same process.
type DNSResolver struct {
	resolver *net.Resolver
}

func NewDNSResolver(servers []string) *DNSResolver {
	if len(servers) == 0 {
		return &DNSResolver{resolver: net.DefaultResolver}
	}

	servers = slices.Clone(servers)
	return &DNSResolver{
		resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				dialer := net.Dialer{Timeout: 10 * time.Second}
				var lastErr error
				for _, server := range servers {
					conn, err := dialer.DialContext(ctx, network, server)
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				return nil, lastErr
			},
		},
	}
}

func (r *DNSResolver) ResolveName(ctx context.Context, host string) ([]netip.Addr, error) {
	ips, err := r.resolver.LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	addrs := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if addr, err := netip.ParseAddr(ip); err == nil {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}

func (r *DNSResolver) ResolveSRV(ctx context.Context, service, proto, name string) (string, uint16, error) {
	_, records, err := r.resolver.LookupSRV(ctx, service, proto, name)
	if err != nil {
		return "", 0, err
	}
	if len(records) == 0 {
		return "", 0, fmt.Errorf("no SRV records found")
	}
	return strings.TrimSuffix(records[0].Target, "."), records[0].Port, nil
}

// DialContext resolves address through this Nylon instance's configured DNS
// servers before dialing it.
func (r *DNSResolver) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := r.ResolveName(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no addresses found for %s", host)
	}

	var dialErr error
	var dialer net.Dialer
	for _, addr := range addrs {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		dialErr = errors.Join(dialErr, err)
	}
	return nil, fmt.Errorf("failed to dial %s: %w", address, dialErr)
}
