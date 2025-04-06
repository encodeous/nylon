//go:build !integration

package impl

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/tun"
	"runtime"
	"strings"
)

func NewWireGuardDevice(s *state.State, n *Nylon) (dev *device.Device, realItf string, err error) {
	if s.UseSystemRouting {
		err = VerifyForwarding()
		if err != nil {
			s.Log.Warn("IP Forwarding is not enabled, routing disabled", "err", err.Error())
			s.DisableRouting = true
		}
	}

	itfName := "nylon" // attempt to name the interface

	if runtime.GOOS == "darwin" {
		itfName = "utun"
	}

	tdev, err := tun.CreateTUN(itfName, device.DefaultMTU)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create TUN: %v. Check if an interface with the name nylon exists already", err)
	}
	realInterfaceName, err := tdev.Name()
	if err == nil {
		itfName = realInterfaceName
	}

	// setup WireGuard
	dev = device.NewDevice(tdev, conn.NewStdNetBind(), &device.Logger{
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

	// start uapi for wg command
	n.wgUapi, err = InitUAPI(itfName)
	if err != nil {
		return nil, "", err
	}

	go func() {
		for s.Context.Err() == nil {
			accept, err := n.wgUapi.Accept()
			if err != nil {
				s.Env.Log.Debug(err.Error())
				continue
			}
			go dev.IpcHandle(accept)
		}
	}()

	s.Log.Info("Created WireGuard interface", "name", itfName)
	return dev, itfName, nil
}

func CleanupWireGuardDevice(s *state.State, n *Nylon) error {
	n.Device.Close()
	err := n.wgUapi.Close()
	return err
}
