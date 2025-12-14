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

func ConfigureRoute(logger *slog.Logger, dev tun.Device, itfName string, route netip.Prefix) error {
	return Exec(logger, "/sbin/route", "-n", "add", "-net", route.String(), "-interface", itfName)
}
