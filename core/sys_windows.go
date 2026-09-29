package core

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/polyamide/tun"
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

func NewSystemRoutes(logger *slog.Logger, dev tun.Device) SystemRoutes {
	return &commandSystemRoutes{logger: logger, dev: dev}
}

type windowsAddress struct {
	IPAddress    string `json:"IPAddress"`
	PrefixLength int    `json:"PrefixLength"`
}

type windowsRoute struct {
	DestinationPrefix string `json:"DestinationPrefix"`
	Protocol          string `json:"Protocol"`
}

func decodePowerShellObjects[T any](out []byte) ([]T, error) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	var values []T
	if out[0] == '[' {
		return values, json.Unmarshal(out, &values)
	}
	var value T
	if err := json.Unmarshal(out, &value); err != nil {
		return nil, err
	}
	return []T{value}, nil
}

func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func (s *commandSystemRoutes) InterfaceAddresses(ifName string) ([]netip.Prefix, error) {
	script := "Get-NetIPAddress -InterfaceAlias " + powerShellQuote(ifName) +
		" | Select-Object IPAddress,PrefixLength | ConvertTo-Json -Compress"
	out, err := ExecOutput(s.logger, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return nil, err
	}
	addresses, err := decodePowerShellObjects[windowsAddress](out)
	if err != nil {
		return nil, err
	}
	prefixes := make([]netip.Prefix, 0, len(addresses))
	for _, address := range addresses {
		addr, err := netip.ParseAddr(strings.Split(address.IPAddress, "%")[0])
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, netip.PrefixFrom(addr, address.PrefixLength))
	}
	return prefixes, nil
}

func (s *commandSystemRoutes) AddAddress(ifName string, addr netip.Addr) error {
	if addr.Is6() {
		return Exec(s.logger, "netsh", "interface", "ipv6", "add", "address", ifName, addr.String())
	}
	return Exec(s.logger, "netsh", "interface", "ip", "add", "address", ifName, addr.String())
}

func (s *commandSystemRoutes) DeleteAddress(ifName string, addr netip.Addr) error {
	if addr.Is6() {
		return Exec(s.logger, "netsh", "interface", "ipv6", "delete", "address", ifName, addr.String())
	}
	return Exec(s.logger, "netsh", "interface", "ip", "delete", "address", ifName, addr.String())
}

func (s *commandSystemRoutes) InterfaceRoutes(ifName string) ([]netip.Prefix, error) {
	script := "Get-NetRoute -InterfaceAlias " + powerShellQuote(ifName) +
		" | Select-Object DestinationPrefix,@{Name='Protocol';Expression={$_.Protocol.ToString()}}" +
		" | ConvertTo-Json -Compress"
	out, err := ExecOutput(s.logger, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if err != nil {
		return nil, err
	}
	routes, err := decodePowerShellObjects[windowsRoute](out)
	if err != nil {
		return nil, err
	}
	prefixes := make([]netip.Prefix, 0, len(routes))
	for _, route := range routes {
		if route.Protocol == "Local" {
			continue
		}
		prefix, err := netip.ParsePrefix(route.DestinationPrefix)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func (s *commandSystemRoutes) AddRoute(itfName string, route netip.Prefix) error {
	ifId := wintypes.LUID((s.dev.(*tun.NativeTun)).LUID())
	itf, err := ifId.Interface()
	if err != nil {
		return err
	}
	ifIndex := strconv.FormatUint(uint64(itf.InterfaceIndex), 10)

	if route.Addr().Is6() {
		return Exec(s.logger, "route", "add", route.String(), "::", "IF", ifIndex)
	} else {
		addr := route.Addr()
		_, mask, _ := net.ParseCIDR(route.String())
		maskStr := net.IP(mask.Mask).String()
		return Exec(s.logger, "route", "add", addr.String(), "mask", maskStr, "0.0.0.0", "IF", ifIndex)
	}
}

func (s *commandSystemRoutes) DeleteRoute(itfName string, route netip.Prefix) error {
	ifId := wintypes.LUID((s.dev.(*tun.NativeTun)).LUID())
	itf, err := ifId.Interface()
	if err != nil {
		return err
	}
	ifIndex := strconv.FormatUint(uint64(itf.InterfaceIndex), 10)

	if route.Addr().Is6() {
		return Exec(s.logger, "route", "delete", route.String(), "::", "IF", ifIndex)
	} else {
		addr := route.Addr()
		_, mask, _ := net.ParseCIDR(route.String())
		maskStr := net.IP(mask.Mask).String()
		return Exec(s.logger, "route", "delete", addr.String(), "mask", maskStr, "0.0.0.0", "IF", ifIndex)
	}
}
