package nylon_dp

import (
	"encoding/hex"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/tun"
	"runtime"
)

type DpUpdates struct {
	Updates string
}

// NyItf is a generic nylon interface, see dp_linux, dp_windows and dp_macos for impl details
type NyItf interface {
	UpdateState(s *state.State, upd *DpUpdates) error
	GetDevice() *device.Device
	Cleanup(s *state.State) error
}

func applyUapiUpdates(dev *device.Device, upd *DpUpdates) error {
	err := dev.IpcSet(upd.Updates)
	if err != nil {
		return err
	}
	return nil
}

func initDevice(s *state.State) (*device.Device, string, error) {
	itfName := "nylon"

	if runtime.GOOS == "darwin" {
		itfName = "utun"
	}

	//addr := s.MustGetNode(s.Id).NylonAddr
	tdev, err := tun.CreateTUN(itfName, device.DefaultMTU)
	//tdev, _, err := netstack.CreateNetTUN([]netip.Addr{
	//	addr,
	//}, make([]netip.Addr, 0), device.DefaultMTU)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create TUN: %v. Check if an interface with the name nylon exists already", err)
	}

	realInterfaceName, err2 := tdev.Name()
	if err2 == nil {
		itfName = realInterfaceName
	}

	dev := device.NewDevice(tdev, conn.NewStdNetBind(), &device.Logger{
		Verbosef: func(format string, args ...any) {
			if state.DBG_log_wireguard {
				s.Log.Debug(fmt.Sprintf(format, args...))
			}
		},
		Errorf: func(format string, args ...any) {
			s.Log.Error(fmt.Sprintf(format, args...))
		},
	})

	err = dev.IpcSet(
		fmt.Sprintf(
			`private_key=%s
listen_port=%d
allow_inbound=true
`,
			hex.EncodeToString(s.WgKey.Bytes()),
			s.DpPort,
		),
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to configure wg device: %v", err)
	}

	return dev, itfName, nil
}
