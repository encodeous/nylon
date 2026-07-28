package core

import (
	"bufio"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/polyamide/tun"
)

func InitUAPI(logger *slog.Logger, itfName string) (net.Listener, error) {
	fileUAPI, err := ipc.UAPIOpen(itfName)

	uapi, err := ipc.UAPIListen(itfName, fileUAPI)
	if err != nil {
		return nil, err
	}
	return uapi, nil
}

func InitInterface(logger *slog.Logger, ifName string) error {
	return nil
}

func NewSystemRoutes(logger *slog.Logger, dev tun.Device) SystemRoutes {
	return &commandSystemRoutes{logger: logger, dev: dev}
}

func (s *commandSystemRoutes) InterfaceAddresses(ifName string) ([]netip.Prefix, error) {
	out, err := ExecOutput(s.logger, "/sbin/ifconfig", ifName)
	if err != nil {
		return nil, err
	}
	var prefixes []netip.Prefix
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || (fields[0] != "inet" && fields[0] != "inet6") {
			continue
		}
		addrText := strings.Split(fields[1], "%")[0]
		addr, err := netip.ParseAddr(addrText)
		if err != nil {
			return nil, err
		}
		bits := addr.BitLen()
		for i, field := range fields {
			if field != "netmask" || i+1 >= len(fields) {
				continue
			}
			mask := strings.TrimPrefix(fields[i+1], "0x")
			value, err := strconv.ParseUint(mask, 16, 64)
			if err == nil && addr.Is4() {
				bits = 0
				for value != 0 {
					bits += int(value & 1)
					value >>= 1
				}
			} else if err == nil && addr.Is6() {
				bits = len(strings.TrimRight(mask, "0")) * 4
			}
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, bits))
	}
	return prefixes, scanner.Err()
}

func (s *commandSystemRoutes) AddAddress(ifName string, addr netip.Addr) error {
	if addr.Is4() {
		return Exec(s.logger, "/sbin/ifconfig", ifName, "alias", addr.String(), "255.255.255.255")
	} else {
		return Exec(s.logger, "/sbin/ifconfig", ifName, "inet6", addr.String(), "alias")
	}
}

func (s *commandSystemRoutes) DeleteAddress(ifName string, addr netip.Addr) error {
	if addr.Is4() {
		return Exec(s.logger, "/sbin/ifconfig", ifName, "-alias", addr.String())
	} else {
		return Exec(s.logger, "/sbin/ifconfig", ifName, "inet6", addr.String(), "-alias")
	}
}

func PrefixToMaskString(p netip.Prefix) string {
	if !p.IsValid() {
		return "Invalid Prefix"
	}

	bits := p.Bits()
	var mask net.IPMask

	if p.Addr().Is4() {
		mask = net.CIDRMask(bits, 32)
	} else if p.Addr().Is6() {
		mask = net.CIDRMask(bits, 128)
	} else {
		// Should not happen for a valid prefix
		return "Unknown IP version"
	}

	// Cast the net.IPMask (a []byte) to net.IP to use its String() method
	return net.IP(mask).String()
}

func (s *commandSystemRoutes) InterfaceRoutes(ifName string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, family := range []string{"inet", "inet6"} {
		out, err := ExecOutput(s.logger, "/usr/sbin/netstat", "-rn", "-f", family)
		if err != nil {
			return nil, err
		}
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[len(fields)-1] != ifName ||
				!strings.Contains(fields[2], "S") {
				continue
			}
			prefix, ok := parseDarwinRoutePrefix(fields[0], family)
			if ok {
				prefixes = append(prefixes, prefix)
			}
		}
		if err := scanner.Err(); err != nil {
			return nil, err
		}
	}
	return prefixes, nil
}

func parseDarwinRoutePrefix(destination, family string) (netip.Prefix, bool) {
	destination = strings.Split(destination, "%")[0]
	if destination == "default" {
		return netip.Prefix{}, false
	}
	if prefix, err := netip.ParsePrefix(destination); err == nil {
		return prefix.Masked(), true
	}
	if addr, err := netip.ParseAddr(destination); err == nil {
		return netip.PrefixFrom(addr, addr.BitLen()), true
	}
	if family == "inet" {
		parts := strings.Split(destination, ".")
		if len(parts) > 0 && len(parts) < 4 {
			bits := len(parts) * 8
			for len(parts) < 4 {
				parts = append(parts, "0")
			}
			if addr, err := netip.ParseAddr(strings.Join(parts, ".")); err == nil {
				return netip.PrefixFrom(addr, bits), true
			}
		}
	}
	return netip.Prefix{}, false
}

func (s *commandSystemRoutes) AddRoute(itfName string, route netip.Prefix) error {
	if route.Addr().Is6() {
		return Exec(s.logger, "/sbin/route", "-n", "add", "-inet6", route.String(), "-interface", itfName)
	} else {
		addr := route.Addr()
		netmask := PrefixToMaskString(route)
		return Exec(s.logger, "/sbin/route", "-n", "add", "-net", addr.String(), "-netmask", netmask, "-interface", itfName)
	}
}

func (s *commandSystemRoutes) DeleteRoute(itfName string, route netip.Prefix) error {
	if route.Addr().Is6() {
		return Exec(s.logger, "/sbin/route", "-n", "delete", "-inet6", route.String(), "-interface", itfName)
	} else {
		addr := route.Addr()
		netmask := PrefixToMaskString(route)
		return Exec(s.logger, "/sbin/route", "-n", "delete", "-net", addr.String(), "-netmask", netmask, "-interface", itfName)
	}
}
