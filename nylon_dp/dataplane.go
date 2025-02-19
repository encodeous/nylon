package nylon_dp

import (
	"encoding/hex"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/tun"
	"runtime"
	"strings"
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

	tdev, err := tun.CreateTUN(itfName, device.DefaultMTU)
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
			if strings.Contains(format, "Failed to send PolySock packets") {
				return
			}
			s.Log.Error(fmt.Sprintf(format, args...))
		},
	})

	err = dev.IpcSet(
		fmt.Sprintf(
			`private_key=%s
listen_port=%d
allow_inbound=true
`,
			hex.EncodeToString(s.Key),
			s.DpPort,
		),
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to configure wg device: %v", err)
	}

	sb := strings.Builder{}
	for _, neigh := range s.GetPeers() {
		sb.WriteString(fmt.Sprintf("public_key=%s\n", hex.EncodeToString(s.MustGetNode(neigh).PubKey)))
	}
	sb.WriteString("\n")
	err = dev.IpcSet(sb.String())
	if err != nil {
		return nil, "", fmt.Errorf("failed to configure wg device: %v", err)
	}

	return dev, itfName, nil
}
