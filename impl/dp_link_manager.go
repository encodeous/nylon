package impl

import (
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
	"time"
)

type DpLinkMgr struct {
	dataplane     nylon_dp.NyItf
	polySock      *device.PolySock
	allowedIpDiff map[string]state.Node
	endpointDiff  map[uuid.UUID]state.Pair[string, time.Time]
	nyEnv         *state.Env
}

func (w *DpLinkMgr) Receive(packet []byte, endpoint conn.Endpoint, peer *device.Peer) {
	e := w.nyEnv
	pkt := &protocol.Probe{}
	err := proto.Unmarshal(packet, pkt)
	if err != nil {
		return
	}
	for _, node := range e.Nodes {
		if node.Id != e.Id && peer.GetPublicKey().Equals(device.NoisePublicKey(e.MustGetNode(node.Id).PubKey)) {
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

				// send pong
				pktBytes, err := proto.Marshal(res)
				if err != nil {
					return
				}
				w.polySock.Send(pktBytes, endpoint, peer)

				e.Dispatch(func(s *state.State) error {
					handleProbePing(s, lid, node.Id, endpoint)
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
	dev := w.polySock.Device

	routesToNeigh := make(map[state.Node][]*state.Route)
	for _, route := range r.Routes {
		routesToNeigh[route.Nh] = append(routesToNeigh[route.Nh], route)
	}

	// configure peers
	for neigh, routes := range routesToNeigh {
		pcfg := s.MustGetNode(neigh)
		allowedIps := make([]string, 0)
		for _, route := range routes {
			cfg, err := s.GetPubNodeCfg(route.Src.Id)
			if err != nil {
				continue
			}
			_, ipNet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", cfg.NylonAddr, cfg.NylonAddr.BitLen())) // TODO: add support for prefixes
			if err != nil {
				continue
			}
			allowedIps = append(allowedIps, ipNet.String())
		}
		sort.Strings(allowedIps)

		peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		for _, allowedIp := range allowedIps {
			dev.Allowedips.Insert(netip.MustParsePrefix(allowedIp), peer)
		}
	}

	for _, neigh := range s.GetPeers() {
		pcfg := s.MustGetNode(neigh)
		nhNeigh := r.GetNeighbour(neigh)
		eps := make([]conn.Endpoint, 0)

		if nhNeigh != nil {
			links := slices.Clone(nhNeigh.DpLinks)
			slices.SortStableFunc(links, func(a, b state.DpLink) int {
				return -int(a.Metric() - b.Metric())
			})
			for _, ep := range links {
				eps = append(eps, ep.Endpoint().GetWgEndpoint())
			}
		}

		// add endpoint if it is not in the list
		for _, ep := range pcfg.DpAddr {
			if !slices.Contains(eps, ep.GetWgEndpoint()) {
				eps = append(eps, ep.GetWgEndpoint())
			}
		}

		peer := dev.LookupPeer(device.NoisePublicKey(pcfg.PubKey))
		peer.SetEndpoints(eps)
	}
	return nil
}

func (w *DpLinkMgr) Init(s *state.State) error {
	w.nyEnv = s.Env
	w.endpointDiff = make(map[uuid.UUID]state.Pair[string, time.Time])
	w.allowedIpDiff = make(map[string]state.Node)
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

	s.RepeatTask(func(s *state.State) error {
		return probeLinks(s, true)
	}, ProbeDpDelay)
	s.RepeatTask(func(s *state.State) error {
		return probeLinks(s, false)
	}, ProbeDpInactiveDelay)
	s.RepeatTask(probeNew, ProbeNewDpDelay)
	s.RepeatTask(UpdateWireGuard, ProbeDpDelay)
	return nil
}
