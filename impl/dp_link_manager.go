package impl

import (
	"encoding/hex"
	"fmt"
	"github.com/encodeous/nylon/nylon_dp"
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"net"
	"sort"
	"strings"
	"time"
)

type DpLinkMgr struct {
	dataplane     nylon_dp.NyItf
	udpSock       *net.UDPConn
	allowedIpDiff map[string]string
	endpointDiff  map[uuid.UUID]state.Pair[string, time.Time]
}

func (w *DpLinkMgr) Cleanup(s *state.State) error {
	s.Log.Info("cleaning up wireguard")
	s.PingBuf.Stop()
	w.udpSock.Close()
	return w.dataplane.Cleanup(s)
}

func UpdateWireGuard(s *state.State) error {
	w := Get[*DpLinkMgr](s)

	r := Get[*Router](s)

	hopsTo := make(map[state.Node][]state.Node)

	for _, route := range r.Routes {
		hopsTo[route.Nh] = append(hopsTo[route.Nh], route.Src.Id)
	}
	sb := new(strings.Builder)

	// configure peers
	for _, route := range r.Routes {
		if hopsTo[route.Nh] != nil {
			allowedIps := make([]string, 0)
			for _, src := range hopsTo[route.Nh] {
				cfg, err := s.GetPubNodeCfg(src)
				if err != nil {
					continue
				}
				_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", cfg.NylonAddr, cfg.NylonAddr.BitLen()))
				if err != nil {
					continue
				}
				allowedIps = append(allowedIps, ipNet.String())
			}
			sort.Strings(allowedIps)
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
			pkey := hex.EncodeToString(pcfg.DpPubKey.Bytes())
			sb.WriteString(fmt.Sprintf("public_key=%s\n", pkey))

			if ep != nil {
				if x, ok := w.endpointDiff[route.Link.Id()]; !ok || x.V1 != ep.String() {
					sb.WriteString(fmt.Sprintf("endpoint=%s\n", ep))
					sb.WriteString(fmt.Sprintf("persistent_keepalive_interval=25\n"))
					w.endpointDiff[route.Link.Id()] = state.Pair[string, time.Time]{ep.String(), time.Now()}
				}
			}

			for _, ip := range allowedIps {
				if w.allowedIpDiff[ip] != pkey {
					w.allowedIpDiff[ip] = pkey
					sb.WriteString(fmt.Sprintf("allowed_ip=%s\n", ip))
				}
			}
		}
	}

	err := w.dataplane.UpdateState(s, &nylon_dp.DpUpdates{Updates: sb.String()})
	if err != nil {
		return err
	}

	return nil
}

func (w *DpLinkMgr) Init(s *state.State) error {
	w.endpointDiff = make(map[uuid.UUID]state.Pair[string, time.Time])
	w.allowedIpDiff = make(map[string]string)
	s.Log.Info("initializing Data Plane")

	dp, err := nylon_dp.NewItf(s)
	if err != nil {
		return fmt.Errorf("could not initialize interface: %v", err)
	}
	w.dataplane = dp

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
