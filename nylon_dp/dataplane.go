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

	dev.DisableSomeRoamingForBrokenMobileSemantics()

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

	for _, neigh := range s.GetPeers() {
		s.Log.Debug("", "neigh", neigh)
		ncfg := s.MustGetNode(neigh)
		peer, err := dev.NewPeer(device.NoisePublicKey(ncfg.PubKey))
		if err != nil {
			return nil, "", err
		}
		peer.Start()
		endpoints := make([]conn.Endpoint, 0)
		for _, nep := range ncfg.DpAddr {
			endpoints = append(endpoints, nep.GetWgEndpoint())
		}
		peer.SetEndpoints(endpoints)
	}
	return dev, itfName, nil
}
