package core

import (
	"log/slog"
	"net"
	"net/netip"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
)

func InitUAPI(e *state.Env, itfName string) (net.Listener, error) {
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

func ConfigureAlias(logger *slog.Logger, ifName string, addr netip.Addr) error {
	if addr.Is4() {
		return Exec(logger, "/sbin/ifconfig", ifName, "alias", addr.String(), "255.255.255.255")
	} else {
		return Exec(logger, "/sbin/ifconfig", ifName, "inet6", addr.String(), "alias")
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

func ConfigureRoute(logger *slog.Logger, dev tun.Device, itfName string, route netip.Prefix) error {
	if route.Addr().Is6() {
		return Exec(logger, "/sbin/route", "-n", "add", "-inet6", route.String(), "-interface", itfName)
	} else {
		addr := route.Addr()
		netmask := PrefixToMaskString(route)
		return Exec(logger, "/sbin/route", "-n", "add", "-net", addr.String(), "-netmask", netmask, "-interface", itfName)
	}
}

func RemoveRoute(logger *slog.Logger, dev tun.Device, itfName string, route netip.Prefix) error {
	if route.Addr().Is6() {
		return Exec(logger, "/sbin/route", "-n", "delete", "-inet6", route.String(), "-interface", itfName)
	} else {
		addr := route.Addr()
		netmask := PrefixToMaskString(route)
		return Exec(logger, "/sbin/route", "-n", "delete", "-net", addr.String(), "-netmask", netmask, "-interface", itfName)
	}
}
