//go:build !integration

package core

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/encodeous/nylon/log"
	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/polyamide/tun"
	"github.com/encodeous/nylon/state"
)

func NewWireGuardDevice(n *Nylon) (dev *device.Device, tunDevice tun.Device, realItf string, err error) {
	itfName := n.InterfaceName // attempt to name the interface

	if runtime.GOOS == "darwin" {
		itfName = "utun"
	}

	tdev, err := tun.CreateTUN(itfName, device.DefaultMTU)
	if err != nil {
		return nil, nil, "", fmt.Errorf("failed to create TUN: %v. Check if an interface with the name nylon exists already", err)
	}
	realInterfaceName, err := tdev.Name()
	if err == nil {
		itfName = realInterfaceName
	}

	wgLog := n.Log.With("module", log.ScopePolyamide)

	// setup WireGuard
	dev = device.NewDevice(tdev, conn.NewDefaultBind(), &device.Logger{
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

	// start uapi for wg command
	n.wgUapi, err = InitUAPI(n.Log, itfName)
	if err != nil {
		return nil, nil, "", err
	}

	if n.wgUapi != nil {
		go func() {
			for n.Context.Err() == nil {
				accept, err := n.wgUapi.Accept()
				if err != nil {
					n.Log.Debug(err.Error())
					continue
				}
				go dev.IpcHandle(accept)
			}
		}()
	}

	n.Log.Info("Created WireGuard interface", "name", itfName)
	return dev, tdev, itfName, nil
}

func CleanupWireGuardDevice(n *Nylon) error {
	n.Device.Close()
	if n.wgUapi != nil {
		err := n.wgUapi.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
