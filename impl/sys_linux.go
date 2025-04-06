package impl

import (
	"fmt"
	"net"
	"net/netip"
	"os"
)

func VerifyForwarding() error {
	forward, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return err
	}
	if string(forward) != "1\n" {
		return fmt.Errorf("expected /proc/sys/net/ipv4/ip_forward = 1 got %s", string(forward))
	}
	// TODO: IPv6 forwarding
	return nil
}

func InitUAPI(itfName string) (net.Listener, error) {
	fileUAPI, err := ipc.UAPIOpen(itfName)

	uapi, err := ipc.UAPIListen(itfName, fileUAPI)
	if err != nil {
		return nil, err
	}
	return uapi, nil
}

func InitInterface(ifName string) error {
	err := Exec("ip", "link", "set", ifName, "up")
	if err != nil {
		return err
	}
	return nil
}

func ConfigureAlias(ifName string, prefix netip.Prefix) error {
	return Exec("ip", "addr", "add", "dev", ifName, prefix.String())
}

func ConfigureRoute(ifName string, route netip.Prefix, via netip.Addr) error {
	err := Exec("ip", "route", "flush", route.String())
	if err != nil {
		return err
	}
	return Exec("ip", "route", "add", route.String(), "via", via.String(), "dev", ifName)
}
