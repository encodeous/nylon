package core

import (
	"log/slog"
	"net/netip"

	"github.com/encodeous/nylon/polyamide/tun"
)

// SystemRoutes provides an authoritative view of addresses and routes attached
// to an interface. Implementations must read the operating system on every
// Interface* call; callers use that state to recover from external changes and
// partially failed commands.
type SystemRoutes interface {
	InterfaceAddresses(ifName string) ([]netip.Prefix, error)
	AddAddress(ifName string, addr netip.Addr) error
	DeleteAddress(ifName string, addr netip.Addr) error

	InterfaceRoutes(ifName string) ([]netip.Prefix, error)
	AddRoute(ifName string, prefix netip.Prefix) error
	DeleteRoute(ifName string, prefix netip.Prefix) error
}

type commandSystemRoutes struct {
	logger *slog.Logger
	dev    tun.Device
}
