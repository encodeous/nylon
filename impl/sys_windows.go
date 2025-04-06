package impl

import (
	"fmt"
	"github.com/encodeous/polyamide/ipc"
	"net"
	"net/netip"
)

func VerifyForwarding() error {
	return fmt.Errorf("Not implemented for Windows")
}

func InitUAPI(itfName string) (net.Listener, error) {
	uapi, err := ipc.UAPIListen(itfName)
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

func ConfigureRoute(ifName string, route netip.Prefix, via netip.Addr) error {
	addr := route.Addr()
	_, mask, _ := net.ParseCIDR(route.String())
	maskStr := net.IP(mask.Mask).String()
	return Exec("route", "add", addr.String(), maskStr, via.String())
}
