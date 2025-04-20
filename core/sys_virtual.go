//go:build integration

package core

import (
	"fmt"
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
	"strings"
)

type VirtualNet interface {
	Bind(node state.NodeId) conn.Bind
	Tun(node state.NodeId) tun.Device
}

func NewWireGuardDevice(s *state.State, n *Nylon) (dev *device.Device, tunDevice tun.Device, realItf string, err error) {
	x := s.AuxConfig["vnet"]
	if x == nil {
		return nil, nil, "", fmt.Errorf("expected aux config \"vnet\", but it was not present")
	}
	vn := x.(VirtualNet)

	itfName := "nylon-vn"

	bind := vn.Bind(s.Id)
	tdev := vn.Tun(s.Id)

	// setup WireGuard
	dev = device.NewDevice(tdev, bind, &device.Logger{
		Verbosef: func(format string, args ...any) {
			if state.DBG_log_wireguard {
				s.Log.Debug(fmt.Sprintf(format, args...))
			}
		},
		Errorf: func(format string, args ...any) {
			if strings.Contains(format, "Failed to send PolySock packets") {
				return
			}
			s.Log.Error(fmt.Sprintf(format, args...))
		},
	})

	s.Log.Info("Created WireGuard interface", "name", itfName)
	return dev, tdev, itfName, nil
}

func CleanupWireGuardDevice(s *state.State, n *Nylon) error {
	if n.Device != nil {
		err := n.Device.Bind().Close()
		if err != nil {
			return err
		}
		n.Device.Close()
	}
	if n.wgUapi != nil {
		err := n.wgUapi.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
