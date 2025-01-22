package impl

import (
	"fmt"
	"github.com/encodeous/nylon/dp_wireguard"
	"github.com/encodeous/nylon/state"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"net"
	"slices"
)

type DpLinkMgr struct {
	client     *wgctrl.Client
	deviceName string
}

func (w *DpLinkMgr) Cleanup(s *state.State) error {
	s.Log.Info("cleaning up wireguard")
	return dp_wireguard.CleanupWireGuard(s)
}

func (w *DpLinkMgr) getWgDevice(s *state.State) (*wgtypes.Device, error) {
	dev, err := w.client.Devices()
	if err != nil {
		return nil, err
	}
	var dpDevice *wgtypes.Device
	// check before init
	for _, d := range dev {
		if slices.Equal(d.PrivateKey[:], s.WgKey.Bytes()) {
			dpDevice = d
		}
	}
	if dpDevice == nil {
		return nil, fmt.Errorf("device not found")
	}
	return dpDevice, nil
}

func (w *DpLinkMgr) Init(s *state.State) error {
	s.Log.Info("initializing wireguard")

	client, err := wgctrl.New()
	if err != nil {
		return err
	}
	w.client = client

	dev, err := w.getWgDevice(s)
	if err != nil {
		err := dp_wireguard.InitWireGuard(s)
		if err != nil {
			return err
		}
		dev, err = w.getWgDevice(s)
	}

	if dev == nil {
		return fmt.Errorf("WireGuard device was not successfully initialized")
	}

	peers := make([]wgtypes.PeerConfig, 0)

	//configure peers
	for _, peer := range s.GetPeers() {
		pcfg, err := s.GetPubNodeCfg(peer)
		if err != nil {
			return err
		}
		peers = append(peers, wgtypes.PeerConfig{
			PublicKey:    wgtypes.Key(pcfg.DpPubKey.Bytes()),
			Remove:       false,
			UpdateOnly:   false,
			PresharedKey: nil,
			Endpoint: &net.UDPAddr{
				IP:   net.ParseIP(pcfg.DpAddr),
				Port: pcfg.DpPort,
			},
			AllowedIPs: nil,
		})
	}

	cfg := wgtypes.Config{
		ListenPort:   &s.WgPort,
		ReplacePeers: true,
		Peers:        peers,
	}

	err = client.ConfigureDevice(dev.Name, cfg)
	if err != nil {
		return err
	}

	//	name := "nylon-" + string(s.Id)
	//	if runtime.GOOS == "darwin" {
	//		name = "utun" + strconv.Itoa(int(hash(string(s.Id)))%1000)
	//	}
	//	tdev, err := tun.CreateTUN(name, device.DefaultMTU)
	//
	//	//nAddr := s.Key.Pubkey().DeriveNylonAddr()
	//	//tdev, _, err := netstack.CreateNetTUN([]netip.Addr{
	//	//	netip.AddrFrom16([16]byte(nAddr)),
	//	//}, make([]netip.Addr, 0), device.DefaultMTU)
	//
	//	if err != nil {
	//		return err
	//	}
	//	itfName, err := tdev.Name()
	//	if err != nil {
	//		return err
	//	}
	//
	//	log := &device.Logger{
	//		Verbosef: func(format string, args ...any) {
	//			s.Log.Debug(fmt.Sprintf(format, args...))
	//		},
	//		Errorf: func(format string, args ...any) {
	//			s.Log.Error(fmt.Sprintf(format, args...))
	//		},
	//	}
	//
	//	s.Log.Info("created tun device", "interface", itfName)
	//	w.device = device.NewDevice(tdev, conn.NewDefaultBind(), log)
	//	w.tdev = &tdev
	//
	//	privkey := hex.EncodeToString(((*ecdh.PrivateKey)(s.WgKey)).Bytes())
	//
	//	err = w.device.IpcSet(fmt.Sprintf("private_key=%s", privkey))
	//	if err != nil {
	//		return err
	//	}
	//	err = w.device.IpcSet(fmt.Sprintf("listen_port=%d", s.WgPort))
	//	if err != nil {
	//		return err
	//	}
	//
	//	s.Log.Info("nylon address is ", "addr", s.Key.Pubkey().DeriveNylonAddr().String())
	//
	//	fileUAPI, err := ipc.UAPIOpen(itfName)
	//	uapi, err := ipc.UAPIListen(itfName, fileUAPI)
	//
	//	go func() {
	//		for {
	//			conn, err := uapi.Accept()
	//			if err != nil {
	//				s.Log.Error(err.Error())
	//				return
	//			}
	//			go w.device.IpcHandle(conn)
	//		}
	//	}()
	//
	//	// configure peers
	//	for _, peer := range s.GetPeers() {
	//		pcfg, err := s.GetPubNodeCfg(peer)
	//		if err != nil {
	//			return err
	//		}
	//		err = w.device.IpcSet(fmt.Sprintf(`public_key=%s
	//endpoint=%s:%d
	//allowed_ip=%s/128
	//`,
	//			hex.EncodeToString((*ecdh.PublicKey)(pcfg.DpPubKey).Bytes()),
	//			pcfg.DpAddr,
	//			pcfg.DpPort,
	//			pcfg.PubKey.DeriveNylonAddr().String(),
	//		))
	//		if err != nil {
	//			return err
	//		}
	//	}

	// https://github.com/libp2p/go-netroute?tab=readme-ov-file
	return nil
}
