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
	err := Exec(logger, "ip", "link", "set", ifName, "up")
	if err != nil {
		return err
	}
	return nil
}

func ConfigureAlias(logger *slog.Logger, ifName string, addr netip.Addr) error {
	return Exec(logger, "ip", "addr", "add", addr.String(), "dev", ifName)
}

func ConfigureRoute(logger *slog.Logger, dev tun.Device, itfName string, route netip.Prefix) error {
	return Exec(logger, "ip", "route", "add", route.String(), "dev", itfName)
}
