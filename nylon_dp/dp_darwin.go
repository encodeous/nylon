package nylon_dp

import (
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/ipc"
	"net"
)

type NyItfMacos struct {
	dev      *device.Device
	realName string
}

func (n *NyItfMacos) GetDevice() *device.Device {
	return n.dev
}

func NewItf(s *state.State) (NyItf, error) {
	itf := NyItfMacos{}
	dev, name, err := initDevice(s)
	if err != nil {
		return nil, err
	}
	itf.dev = dev
	itf.realName = name

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

	s.Log.Info("Created WireGuard interface", "name", itf.realName)

	selfPrefixes := s.MustGetNode(s.Id).Prefixes

	if len(selfPrefixes) != 0 {
		// configure system
		// assign ip
		for _, prefix := range selfPrefixes {
			addr := prefix.Addr()
			if addr.Is4() {
				_, mask, _ := net.ParseCIDR(prefix.String())
				err = Exec("/sbin/ifconfig", name, "alias", addr.String(), addr.String(), "netmask", net.IP(mask.Mask).String())
			} else {
				err = Exec("/sbin/ifconfig", name, "inet6", prefix.String(), "add")
			}
			if err != nil {
				return nil, err
			}
		}

		for _, peer := range s.CentralCfg.Nodes {
			if peer.Id == s.Id {
				continue
			}
			for _, prefix := range peer.Prefixes {
				err = Exec("/sbin/route", "-n", "add", "-net", prefix.String(), selfPrefixes[0].Addr().String())
				if err != nil {
					return nil, err
				}
			}
		}
	}

	return &itf, nil
}

func (n *NyItfMacos) Cleanup(s *state.State) error {
	n.dev.Close()
	return nil
}
