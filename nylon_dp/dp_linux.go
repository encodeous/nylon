package nylon_dp

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/ipc"
)

type NyItfLinux struct {
	dev *device.Device
}

func (n *NyItfLinux) GetDevice() *device.Device {
	return n.dev
}

func (n *NyItfLinux) UpdateState(s *state.State, upd *DpUpdates) error {
	return applyUapiUpdates(n.dev, upd)
}

func (n *NyItfLinux) Cleanup(s *state.State) error {
	n.dev.Close()
	return nil
}

func NewItf(s *state.State) (NyItf, error) {
	itf := NyItfLinux{}

	dev, name, err := initDevice(s)
	if err != nil {
		return nil, fmt.Errorf("failed to init device: %v", err)
	}
	itf.dev = dev

	fileUAPI, err := ipc.UAPIOpen(name)

	uapi, err := ipc.UAPIListen(name, fileUAPI)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			conn, err := uapi.Accept()
			if err != nil {
				s.Env.Log.Error(err.Error())
			}
			go dev.IpcHandle(conn)
		}
	}()

	s.Log.Info("Created WireGuard interface", "name", name)

	// bring up the interface, if it was down
	err = Exec("/usr/bin/ip", "link", "set", name, "up")
	if err != nil {
		return nil, err
	}

	// assign ip
	selfPrefixes := s.MustGetNode(s.Id).Prefixes

	if len(selfPrefixes) != 0 {
		for _, prefix := range selfPrefixes {
			err = Exec("/usr/bin/ip", "addr", "add", "dev", name, prefix.String())
			if err != nil {
				return nil, err
			}
		}

		for _, peer := range s.CentralCfg.Nodes {
			if peer.Id == s.Id {
				continue
			}
			for _, prefix := range peer.Prefixes {
				err = Exec("/usr/bin/ip", "route", "flush", prefix.String())
				if err != nil {
					return nil, err
				}
				err = Exec("/usr/bin/ip", "route", "add", prefix.String(), "via", selfPrefixes[0].Addr().String(), "dev", name)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	// disable icmp redirect for ipv4
	err = Exec("/usr/sbin/sysctl", fmt.Sprintf("net.ipv4.conf.%s.send_redirects=0", name))
	if err != nil {
		return nil, err
	}

	return &itf, nil
}
