package impl

import (
	"encoding/hex"
	"fmt"
	"github.com/encodeous/nylon/nylon_dp"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"google.golang.org/protobuf/proto"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"
)

type DpLinkMgr struct {
	dataplane     nylon_dp.NyItf
	polySock      *device.PolySock
	allowedIpDiff map[string]string
	endpointDiff  map[uuid.UUID]state.Pair[string, time.Time]
	nyEnv         *state.Env
}

func (w *DpLinkMgr) Receive(packet []byte, endpoint conn.Endpoint) {
	e := w.nyEnv
	pkt := &protocol.Probe{}
	err := proto.Unmarshal(packet, pkt)
	if err != nil {
		return
	}
	tok := pkt.Token
	if pkt.ResponseToken != nil {
		tok = *pkt.ResponseToken
	}
	for _, node := range e.Nodes {
		if node.Id != e.Id && slices.Equal(generateAnonHash(tok, node.PubKey), pkt.NodeId) {
			lid, err := uuid.FromBytes(pkt.LinkId)
			if err != nil {
				return
			}
			if pkt.ResponseToken == nil {
				// ping

				// build pong response
				res := pkt
				token := rand.Uint64()
				res.ResponseToken = &token
				ctime := time.Now().UnixMicro()
				res.ReceptionTime = &ctime
				res.NodeId = generateAnonHash(token, e.Key.XPubkey())

				// send pong
				pktBytes, err := proto.Marshal(res)
				if err != nil {
					return
				}
				w.polySock.Send(pktBytes, endpoint)

				e.Dispatch(func(s *state.State) error {
					handleProbePing(s, lid, node.Id, state.DpEndpoint{
						Name:       fmt.Sprintf("remote-%s-%s", node.Id, endpoint.DstToString()),
						Addr:       netip.MustParseAddrPort(endpoint.DstToString()),
						RemoteInit: true,
					})
					return nil
				})
			} else {
				// pong
				if pkt.ReceptionTime == nil {
					continue
				}
				e.Dispatch(func(s *state.State) error {
					handleProbePong(s, lid, node.Id, pkt.Token, time.UnixMicro(*pkt.ReceptionTime), endpoint)
					return nil
				})
			}
		}
	}
}

func (w *DpLinkMgr) Cleanup(s *state.State) error {
	s.Log.Info("cleaning up wireguard")
	s.PingBuf.Stop()
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
			ep := net.UDPAddrFromAddrPort(route.Link.Endpoint().Addr)
			pkey := hex.EncodeToString(pcfg.PubKey)
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

	sb.WriteString("\n")

	err := w.dataplane.UpdateState(s, &nylon_dp.DpUpdates{Updates: sb.String()})
	if err != nil {
		return err
	}

	return nil
}

func (w *DpLinkMgr) Init(s *state.State) error {
	w.nyEnv = s.Env
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

	wgDev := dp.GetDevice()

	w.polySock = wgDev.PolyListen(w)
	s.Log.Info("started polysock listener")

	s.RepeatTask(probeExisting, ProbeDpDelay)
	s.RepeatTask(probeNew, ProbeNewDpDelay)
	s.RepeatTask(UpdateWireGuard, ProbeDpDelay)
	return nil
}
