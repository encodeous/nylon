package state

import "net/netip"

// DefaultLocalExcludes are addresses that should never travel through an exit
// node. They are link-local / multicast / loopback ranges that have no
// meaning once they leave the originating link, plus mDNS/SSDP that would
// either disrupt local discovery or be silently dropped by the upstream.
//
// Exit-encap filters drop traffic to these destinations before wrapping it
// into a tunnel; iOS / mobile route builders also use the same list to
// populate excluded routes in NEPacketTunnelNetworkSettings.
func DefaultLocalExcludes() []netip.Prefix {
	result := DefaultLocalIPv4Excludes()
	result = append(result,
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("ff00::/8"),
	)
	return result
}

func DefaultLocalIPv4Excludes() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("255.255.255.255/32"),
	}
}

func IsDefaultLocalExcludedAddr(addr netip.Addr) bool {
	for _, prefix := range DefaultLocalExcludes() {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}
