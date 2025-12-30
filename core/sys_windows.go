package core

import (
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/kmahyyg/go-network-compo/wintypes"
)

func InitUAPI(e *state.Env, itfName string) (net.Listener, error) {
	uapi, err := ipc.UAPIListen(itfName)
	if err != nil && strings.Contains(err.Error(), "This security ID may not be assigned as the owner of this object") {
		e.Log.Warn("UAPI not started. Nylon needs to be run with SYSTEM privileges. See: https://github.com/WireGuard/wgctrl-go/issues/141")
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
	return Exec(logger, "netsh", "interface", "ip", "add", "address", ifName, addr.String())
}

func ConfigureRoute(logger *slog.Logger, dev tun.Device, itfName string, route netip.Prefix) error {
	addr := route.Addr()
	_, mask, _ := net.ParseCIDR(route.String())
	maskStr := net.IP(mask.Mask).String()
	ifId := wintypes.LUID((dev.(*tun.NativeTun)).LUID())
	itf, err := ifId.Interface()
	if err != nil {
		return err
	}
	return Exec(logger, "route", "add", addr.String(), "mask", maskStr, "0.0.0.0", "IF", strconv.FormatUint(uint64(itf.InterfaceIndex), 10))
}
