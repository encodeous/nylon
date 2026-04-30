//go:build integration

package core

import (
	"fmt"
	"strings"

	"github.com/encodeous/nylon/log"
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
)

type VirtualNet interface {
	Bind(node state.NodeId) conn.Bind
	Tun(node state.NodeId) tun.Device
}

func NewWireGuardDevice(n *Nylon) (dev *device.Device, tunDevice tun.Device, realItf string, err error) {
	x := n.AuxConfig["vnet"]
	if x == nil {
		return nil, nil, "", fmt.Errorf("expected aux config \"vnet\", but it was not present")
	}
	vn := x.(VirtualNet)

	itfName := "nylon-vn"

	bind := vn.Bind(n.LocalCfg.Id)
	tdev := vn.Tun(n.LocalCfg.Id)

	wgLog := n.Log.With("module", log.ScopePolyamide)

	// setup WireGuard
	dev = device.NewDevice(tdev, bind, &device.Logger{
		Verbosef: func(format string, args ...any) {
			if state.DBG_log_wireguard {
				wgLog.Debug(fmt.Sprintf(format, args...))
			}
		},
		Errorf: func(format string, args ...any) {
			if strings.Contains(format, "Failed to send PolySock packets") {
				return
			}
			wgLog.Error(fmt.Sprintf(format, args...))
		},
	})

	n.Log.Info("Created WireGuard interface", "name", itfName)
	return dev, tdev, itfName, nil
}

func CleanupWireGuardDevice(n *Nylon) error {
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
