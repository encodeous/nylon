package nylon_dp

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/device"
	"github.com/encodeous/polyamide/ipc"
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

	// configure system
	// assign ip
	addr := s.MustGetNode(s.Id).NylonAddr
	if addr.Is4() {
		err = Exec("/sbin/ifconfig", name, "alias", addr.String(), addr.String(), "netmask", "255.255.255.255")
	} else {
		err = Exec("/sbin/ifconfig", name, "inet6", addr.String()+"/128", "add")
	}
	if err != nil {
		return nil, err
	}

	for _, peer := range s.CentralCfg.Nodes {
		if peer.Id == s.Id {
			continue
		}
		err = Exec("/sbin/route", "-n", "add", "-net", fmt.Sprintf("%s/%d", peer.NylonAddr.String(), peer.NylonAddr.BitLen()), addr.String())
		if err != nil {
			return nil, err
		}
	}

	return &itf, nil
}

func (n *NyItfMacos) Cleanup(s *state.State) error {
	n.dev.Close()
	return nil
}
