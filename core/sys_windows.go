package core

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"

	"github.com/encodeous/nylon/polyamide"
	"github.com/kmahyyg/go-network-compo/wintypes"
)

func InitInterface(logger *slog.Logger, ifName string) error {
	return nil
}

func ConfigureAlias(logger *slog.Logger, ifName string, addr netip.Addr) error {
	if addr.Is6() {
		return Exec(logger, "netsh", "interface", "ipv6", "add", "address", ifName, addr.String())
	}
	return Exec(logger, "netsh", "interface", "ip", "add", "address", ifName, addr.String(), "255.255.255.255")
}

func RemoveAlias(logger *slog.Logger, ifName string, addr netip.Addr) error {
	if addr.Is6() {
		return Exec(logger, "netsh", "interface", "ipv6", "delete", "address", ifName, addr.String())
	}
	return Exec(logger, "netsh", "interface", "ip", "delete", "address", ifName, addr.String())
}

func ConfigureRoute(logger *slog.Logger, dev polyamide.HostDevice, itfName string, route netip.Prefix) error {
	native, ok := dev.(interface{ LUID() uint64 })
	if !ok {
		return fmt.Errorf("host device does not expose a Windows LUID")
	}
	ifId := wintypes.LUID(native.LUID())
	itf, err := ifId.Interface()
	if err != nil {
		return err
	}
	ifIndex := strconv.FormatUint(uint64(itf.InterfaceIndex), 10)

	if route.Addr().Is6() {
		return Exec(logger, "route", "add", route.String(), "::", "IF", ifIndex)
	} else {
		addr := route.Addr()
		_, mask, _ := net.ParseCIDR(route.String())
		maskStr := net.IP(mask.Mask).String()
		return Exec(logger, "route", "add", addr.String(), "mask", maskStr, "0.0.0.0", "IF", ifIndex)
	}
}

func RemoveRoute(logger *slog.Logger, dev polyamide.HostDevice, itfName string, route netip.Prefix) error {
	native, ok := dev.(interface{ LUID() uint64 })
	if !ok {
		return fmt.Errorf("host device does not expose a Windows LUID")
	}
	ifId := wintypes.LUID(native.LUID())
	itf, err := ifId.Interface()
	if err != nil {
		return err
	}
	ifIndex := strconv.FormatUint(uint64(itf.InterfaceIndex), 10)

	if route.Addr().Is6() {
		return Exec(logger, "route", "delete", route.String(), "::", "IF", ifIndex)
	} else {
		addr := route.Addr()
		_, mask, _ := net.ParseCIDR(route.String())
		maskStr := net.IP(mask.Mask).String()
		return Exec(logger, "route", "delete", addr.String(), "mask", maskStr, "0.0.0.0", "IF", ifIndex)
	}
}
