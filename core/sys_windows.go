package core

import (
	"fmt"
	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"github.com/kmahyyg/go-network-compo/wintypes"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

func VerifyForwarding() error {
	return fmt.Errorf("Not implemented for Windows")
}

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

func InitInterface(ifName string) error {
	return nil
}

func ConfigureAlias(ifName string, prefix netip.Prefix) error {
	addr := prefix.Addr()
	_, mask, _ := net.ParseCIDR(prefix.String())
	maskStr := net.IP(mask.Mask).String()
	return Exec("netsh", "interface", "ip", "add", "address", ifName, addr.String(), maskStr)
}

func ConfigureRoute(dev tun.Device, itfName string, route netip.Prefix, via netip.Addr) error {
	addr := route.Addr()
	_, mask, _ := net.ParseCIDR(route.String())
	maskStr := net.IP(mask.Mask).String()
	ifId := wintypes.LUID((dev.(*tun.NativeTun)).LUID())
	itf, err := ifId.Interface()
	if err != nil {
		return err
	}
	return Exec("route", "add", addr.String(), "mask", maskStr, via.String(), "IF", strconv.FormatUint(uint64(itf.InterfaceIndex), 10))
}
