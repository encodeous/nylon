package core

import (
	"log/slog"
	"net"
	"net/netip"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide-wireguard-go/ipc"
)

func InitUAPI(logger *slog.Logger, itfName string) (net.Listener, error) {
	fileUAPI, err := ipc.UAPIOpen(itfName)
	if err != nil {
		return nil, err
	}

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
	return Exec(logger, "ip", "addr", "add", state.AddrToPrefix(addr).String(), "dev", ifName)
}

func RemoveAlias(logger *slog.Logger, ifName string, addr netip.Addr) error {
	return Exec(logger, "ip", "addr", "del", state.AddrToPrefix(addr).String(), "dev", ifName)
}

func ConfigureRoute(logger *slog.Logger, dev polyamide.HostDevice, itfName string, route netip.Prefix) error {
	return Exec(logger, "ip", "route", "add", route.String(), "dev", itfName)
}

func RemoveRoute(logger *slog.Logger, dev polyamide.HostDevice, itfName string, route netip.Prefix) error {
	return Exec(logger, "ip", "route", "del", route.String(), "dev", itfName)
}
