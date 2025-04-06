package impl

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/ipc"
	"github.com/encodeous/polyamide/tun"
	"net"
	"net/netip"
	"os/exec"
	"strings"
)

func VerifyForwarding() error {
	res, err := exec.Command("sysctl", "net.inet.ip.forwarding").CombinedOutput()
	if err != nil {
		return err
	}
	if !strings.Contains(string(res), "1") {
		return fmt.Errorf("expected net.inet.ip.forwarding = 1 got %s", string(res))
	}
	return nil
}

func InitUAPI(e *state.Env, itfName string) (net.Listener, error) {
	fileUAPI, err := ipc.UAPIOpen(itfName)

	uapi, err := ipc.UAPIListen(itfName, fileUAPI)
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
	if addr.Is4() {
		_, mask, _ := net.ParseCIDR(prefix.String())
		return Exec("/sbin/ifconfig", ifName, "alias", addr.String(), addr.String(), "netmask", net.IP(mask.Mask).String())
	} else {
		return Exec("/sbin/ifconfig", ifName, "inet6", prefix.String(), "add")
	}
}

func ConfigureRoute(dev tun.Device, itfName string, route netip.Prefix, via netip.Addr) error {
	return Exec("/sbin/route", "-n", "add", "-net", route.String(), via.String())
}
