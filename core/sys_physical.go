//go:build !integration

package core

import (
	"fmt"
	"net"
	"runtime"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/device"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun"
)

func newDefaultRuntime(n *Nylon) (Runtime, error) {
	name := n.InterfaceName
	if runtime.GOOS == "darwin" && !n.NoTun {
		name = "utun"
	}
	var host tun.Device
	var err error
	if n.NoTun {
		host = tun.NewDummyDevice(name)
	} else {
		host, err = tun.CreateTUN(name, device.DefaultMTU)
		if err != nil {
			return Runtime{}, fmt.Errorf("failed to create TUN: %w. Check if an interface with the name %s exists already", err, name)
		}
	}
	transport, err := NewWireGuardTransport(n, nil)
	if err != nil {
		host.Close()
		return Runtime{}, err
	}
	actual, err := host.Name()
	var listener net.Listener
	if err == nil {
		listener, err = InitUAPI(n.Log, actual)
	}
	if err != nil {
		host.Close()
		transport.Close()
		return Runtime{}, err
	}
	n.wireGuard = &legacyWireGuard{transport: transport, listener: listener}
	if n.NoTun {
		n.Log.Info("Created userspace-only WireGuard device", "name", actual)
	} else {
		n.Log.Info("Created WireGuard interface", "name", actual)
	}
	return Runtime{Transports: []polyamide.Transport{transport}, Host: host}, nil
}
