package core

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
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

func NewSystemRoutes(logger *slog.Logger, dev tun.Device) SystemRoutes {
	return &commandSystemRoutes{logger: logger, dev: dev}
}

func (s *commandSystemRoutes) InterfaceAddresses(ifName string) ([]netip.Prefix, error) {
	out, err := ExecOutput(s.logger, "ip", "-json", "address", "show", "dev", ifName)
	if err != nil {
		return nil, err
	}
	var links []struct {
		AddrInfo []struct {
			Family    string `json:"family"`
			Local     string `json:"local"`
			PrefixLen int    `json:"prefixlen"`
		} `json:"addr_info"`
	}
	if err := json.Unmarshal(out, &links); err != nil {
		return nil, err
	}
	var prefixes []netip.Prefix
	for _, link := range links {
		for _, address := range link.AddrInfo {
			if address.Family != "inet" && address.Family != "inet6" {
				continue
			}
			addr, err := netip.ParseAddr(address.Local)
			if err != nil {
				return nil, err
			}
			prefixes = append(prefixes, netip.PrefixFrom(addr, address.PrefixLen))
		}
	}
	return prefixes, nil
}

func (s *commandSystemRoutes) AddAddress(ifName string, addr netip.Addr) error {
	return Exec(s.logger, "ip", "addr", "add", state.AddrToPrefix(addr).String(), "dev", ifName)
}

func (s *commandSystemRoutes) DeleteAddress(ifName string, addr netip.Addr) error {
	return Exec(s.logger, "ip", "addr", "del", state.AddrToPrefix(addr).String(), "dev", ifName)
}

func (s *commandSystemRoutes) InterfaceRoutes(ifName string) ([]netip.Prefix, error) {
	out, err := ExecOutput(s.logger, "ip", "-json", "route", "show", "dev", ifName)
	if err != nil {
		return nil, err
	}
	var routes []struct {
		Destination string `json:"dst"`
		Protocol    string `json:"protocol"`
	}
	if err := json.Unmarshal(out, &routes); err != nil {
		return nil, err
	}
	prefixes := make([]netip.Prefix, 0, len(routes))
	for _, route := range routes {
		// Kernel routes are owned by interface addresses, not Nylon's route
		// reconciler. Deleting one can leave the address present but unusable.
		if route.Protocol == "kernel" || route.Destination == "" || route.Destination == "default" {
			continue
		}
		prefix, err := netip.ParsePrefix(route.Destination)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func (s *commandSystemRoutes) AddRoute(ifName string, route netip.Prefix) error {
	return Exec(s.logger, "ip", "route", "add", route.String(), "dev", ifName)
}

func (s *commandSystemRoutes) DeleteRoute(ifName string, route netip.Prefix) error {
	return Exec(s.logger, "ip", "route", "del", route.String(), "dev", ifName)
}
