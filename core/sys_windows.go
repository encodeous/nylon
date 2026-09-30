package core

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/ipc"
	"github.com/kmahyyg/go-network-compo/wintypes"
)

func InitUAPI(logger *slog.Logger, itfName string) (net.Listener, error) {
	uapi, err := ipc.UAPIListen(itfName)
	if err != nil && strings.Contains(err.Error(), "This security ID may not be assigned as the owner of this object") {
		logger.Warn("UAPI not started. Nylon needs to be run with SYSTEM privileges. See: https://github.com/WireGuard/wgctrl-go/issues/141")
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return uapi, nil
}

func InitInterface(logger *slog.Logger, ifName string) error {
	return nil
}

func ConfigureAlias(logger *slog.Logger, ifName string, addr netip.Addr) error {
	if addr.Is6() {
		return Exec(logger, "netsh", "interface", "ipv6", "add", "address", ifName, addr.String())
	}
	return Exec(logger, "netsh", "interface", "ip", "add", "address", ifName, addr.String())
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
