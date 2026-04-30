package e2e

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

// AllocateDockerSubnet returns a /24 subnet and gateway that do not overlap
// with any currently known Docker network on the local daemon.
func AllocateDockerSubnet(ctx context.Context) (string, string, error) {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return "", "", err
	}
	defer provider.Close()

	networks, err := provider.Client().NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return "", "", err
	}

	var existing []netip.Prefix
	for _, nw := range networks.Items {
		for _, cfg := range nw.IPAM.Config {
			if !cfg.Subnet.IsValid() {
				continue
			}
			if cfg.Subnet.Addr().Is4() {
				existing = append(existing, cfg.Subnet.Masked())
			}
		}
	}

	for second := 200; second <= 250; second++ {
		for third := 0; third <= 255; third++ {
			subnet := fmt.Sprintf("10.%d.%d.0/24", second, third)
			prefix := netip.MustParsePrefix(subnet)

			overlaps := false
			for _, used := range existing {
				if prefix.Overlaps(used) {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}

			return subnet, fmt.Sprintf("10.%d.%d.1", second, third), nil
		}
	}

	return "", "", fmt.Errorf("no free Docker subnet found in test pool")
}

// GetIP returns an IP address within the allocated subnet for a given host suffix.
// It reserves the last octet for the requested suffix, so callers should use small
// host numbers such as 2, 10, 100, etc.
func GetIP(subnet string, suffix int) string {
	prefix, err := netip.ParsePrefix(subnet)
	if err != nil {
		panic(fmt.Sprintf("invalid subnet format: %s", subnet))
	}

	addr := prefix.Addr()
	if !addr.Is4() {
		panic(fmt.Sprintf("unsupported non-IPv4 subnet: %s", subnet))
	}

	bytes := addr.As4()
	bytes[3] = byte(suffix)
	ip := netip.AddrFrom4(bytes)
	if !prefix.Contains(ip) {
		panic(fmt.Sprintf("requested IP %s is outside subnet %s", ip, subnet))
	}
	return ip.String()
}
