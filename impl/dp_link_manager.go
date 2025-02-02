package impl

import (
	"fmt"
	"github.com/encodeous/nylon/dp_wireguard"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"net"
	"slices"
	"sort"
	"time"
)

type DpLinkMgr struct {
	client     *wgctrl.Client
	deviceName string
	udpSock    *net.UDPConn
}

func (w *DpLinkMgr) Cleanup(s *state.State) error {
	s.Log.Info("cleaning up wireguard")
	s.PingBuf.Stop()
	w.udpSock.Close()
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

func UpdateWireGuard(s *state.State) error {
	w := Get[*DpLinkMgr](s)
	r := Get[*Router](s)
	dev, err := w.getWgDevice(s)
	if err != nil {
		return err
	}

	peers := make([]wgtypes.PeerConfig, 0)

	hopsTo := make(map[state.Node][]state.Node)

	for _, route := range r.Routes {
		hopsTo[route.Nh] = append(hopsTo[route.Nh], route.Src.Id)
	}

	// configure peers
	for _, route := range r.Routes {
		if hopsTo[route.Nh] != nil {
			allowedIps := make([]net.IPNet, 0)
			for _, src := range hopsTo[route.Nh] {
				cfg, err := s.GetPubNodeCfg(src)
				if err != nil {
					continue
				}
				_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", cfg.NylonAddr, cfg.NylonAddr.BitLen()))
				if err != nil {
					continue
				}
				allowedIps = append(allowedIps, *ipNet)
			}
			sort.Slice(allowedIps, func(i, j int) bool {
				return slices.Compare(allowedIps[i].IP, allowedIps[j].IP) < 0
			})
			pcfg, err := s.GetPubNodeCfg(route.Nh)
			if err != nil {
				return err
			}
			var ep *net.UDPAddr
			if route.Link.Endpoint().DpAddr != nil {
				ep = net.UDPAddrFromAddrPort(*route.Link.Endpoint().DpAddr)
			} else {
				ep = nil
			}
			dur := time.Second * 25
			peers = append(peers, wgtypes.PeerConfig{
				PublicKey:                   wgtypes.Key(pcfg.DpPubKey.Bytes()),
				Remove:                      false,
				UpdateOnly:                  true,
				PresharedKey:                nil,
				PersistentKeepaliveInterval: &dur,
				Endpoint:                    ep,
				AllowedIPs:                  allowedIps,
			})
		}
	}
	sort.Slice(peers, func(i, j int) bool {
		return slices.Compare(peers[i].PublicKey[:], peers[j].PublicKey[:]) < 0
	})
	cfg := wgtypes.Config{
		ReplacePeers: false,
		Peers:        peers,
	}

	err = w.client.ConfigureDevice(dev.Name, cfg)
	if err != nil {
		return err
	}
	return nil
}

func (w *DpLinkMgr) Init(s *state.State) error {
	s.Log.Info("initializing WireGuard")

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

	portAddr := int(s.DpBind.Port())

	cfg := wgtypes.Config{
		ListenPort: &portAddr,
	}

	err = client.ConfigureDevice(dev.Name, cfg)
	if err != nil {
		return err
	}

	s.PingBuf = ttlcache.New[uint64, state.LinkPing](
		ttlcache.WithTTL[uint64, state.LinkPing](5*time.Second),
		ttlcache.WithDisableTouchOnHit[uint64, state.LinkPing](),
	)
	go s.PingBuf.Start()

	w.udpSock, err = net.ListenUDP("udp", &net.UDPAddr{IP: nil, Port: int(s.ProbeBind.Port())})
	s.Log.Info("started probe sock")
	if err != nil {
		return err
	}

	go probeListener(s.Env, w.udpSock)
	s.RepeatTask(probeDataPlane, ProbeDpDelay)
	s.RepeatTask(UpdateWireGuard, ProbeDpDelay)
	return nil
}
