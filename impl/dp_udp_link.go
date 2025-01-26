package impl

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/rosshemsley/kalman"
	"github.com/rosshemsley/kalman/models"
	"google.golang.org/protobuf/proto"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
	"time"
)

// TODO: Implement history function and other non-ping related metric calculations, i.e packet loss, p95, p99

type UdpDpLink struct {
	id               uuid.UUID
	metric           uint16
	realLatency      time.Duration
	lastMetricUpdate time.Time
	endpoint         state.DpEndpoint
	filter           *kalman.KalmanFilter
	model            *models.SimpleModel
}

func NewUdpDpLink(id uuid.UUID, metric uint16, endpoint state.DpEndpoint) *UdpDpLink {
	model := models.NewSimpleModel(time.Now(), float64(time.Millisecond*50), models.SimpleModelConfig{
		InitialVariance:     0,
		ProcessVariance:     float64(time.Millisecond * 10),
		ObservationVariance: float64(time.Millisecond * 5),
	})
	return &UdpDpLink{
		id:               id,
		metric:           metric,
		endpoint:         endpoint,
		filter:           kalman.NewKalmanFilter(model),
		model:            model,
		lastMetricUpdate: time.Now(),
	}
}

func (u *UdpDpLink) Endpoint() state.DpEndpoint {
	return u.endpoint
}

func (u *UdpDpLink) UpdatePing(ping time.Duration) {
	err := u.filter.Update(time.Now(), u.model.NewMeasurement(float64(ping)))
	if err != nil {
		return
	}

	u.realLatency = ping
	filtered := u.model.Value(u.filter.State())

	// latency in steps of 5 milliseconds
	latencyContrib := time.Duration(filtered).Milliseconds() * 10

	u.metric = uint16(min(max(latencyContrib, 1), int64(INF)))
	u.metric = uint16(min(max(int64(u.metric), 1), int64(INF)))

	//slog.Info("lu", "r", u.realLatency, "f", time.Duration(filtered))

	u.lastMetricUpdate = time.Now()
}

func (u *UdpDpLink) Id() uuid.UUID {
	return u.id
}

func (u *UdpDpLink) Metric() uint16 {
	// if no pings for the past 3s, we return INF
	if u.lastMetricUpdate.Before(time.Now().Add(-time.Second * 3)) {
		return INF
	}
	return u.metric
}

func (u *UdpDpLink) IsRemote() bool {
	return u.endpoint.DpAddr == nil
}

// region probe io
func generateAnonHash(token uint64, pubKey state.EdPublicKey) []byte {
	hash := sha256.Sum256(binary.LittleEndian.AppendUint64(pubKey, token))
	return hash[:]
}

func probeListener(e *state.Env, sock *net.UDPConn) {
	for e.Context.Err() == nil {
		buf := make([]byte, 256)
		n, addrport, err := sock.ReadFromUDPAddrPort(buf)
		if err != nil {
			continue
		}

		go func() {
			pkt := &protocol.Probe{}
			err := proto.Unmarshal(buf[:n], pkt)
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
						// dTODO: Remove after debugging
						//weight := state.GetMinMockWeight(e.Id, node.Id, e.CentralCfg)
						//if weight == 0 {
						//	return
						//}
						//time.Sleep(weight)

						// build pong response
						res := pkt
						token := rand.Uint64()
						res.ResponseToken = &token
						res.NodeId = generateAnonHash(token, e.Key.Pubkey())

						pktBytes, err := proto.Marshal(res)
						if err != nil {
							return
						}
						_, err = sock.WriteToUDPAddrPort(pktBytes, addrport)
						if err != nil {
							return
						}

						if err != nil {
							return
						}
						e.Dispatch(func(s *state.State) error {
							handleProbePing(s, lid, node.Id, state.DpEndpoint{
								Name:      fmt.Sprintf("remote-%s-%s", node.Id, lid.String()),
								DpAddr:    nil,
								ProbeAddr: &addrport,
							})
							return nil
						})
					} else {
						// pong
						e.Dispatch(func(s *state.State) error {
							handleProbePong(s, lid, node.Id, pkt.Token)
							return nil
						})
					}
				}
			}
		}()
	}
}

func probe(e *state.Env, sock *net.UDPConn, addr netip.AddrPort, linkId uuid.UUID) error {
	token := rand.Uint64()
	uid, err := linkId.MarshalBinary()
	if err != nil {
		return err
	}
	ping := &protocol.Probe{
		Token:         token,
		ResponseToken: nil,
		NodeId:        generateAnonHash(token, e.Key.Pubkey()),
		LinkId:        uid,
	}
	marshal, err := proto.Marshal(ping)
	if err != nil {
		return err
	}
	_, err = sock.WriteToUDPAddrPort(marshal, addr)
	if err != nil {
		return err
	}
	e.PingBuf.Set(token, state.LinkPing{
		LinkId: linkId,
		Time:   time.Now(),
	}, ttlcache.DefaultTTL)
	return nil
}

// endregion probe io

func handleProbePing(s *state.State, link uuid.UUID, node state.Node, endpoint state.DpEndpoint) {
	if node == s.Id {
		return
	}
	// check if link exists
	r := Get[*Router](s)
	for _, neigh := range r.Neighbours {
		for _, dpLink := range neigh.DpLinks {
			if dpLink.Id() == link && neigh.Id == node {
				// we have a link
				return
			}
		}
	}
	// create a new link if we dont have a link
	for _, neigh := range r.Neighbours {
		if neigh.Id == node {
			neigh.DpLinks = append(neigh.DpLinks, NewUdpDpLink(link, INF, endpoint))
			return
		}
	}
	return
}

func handleProbePong(s *state.State, link uuid.UUID, node state.Node, token uint64) {
	// check if link exists
	r := Get[*Router](s)
	for _, neigh := range r.Neighbours {
		for _, dpLink := range neigh.DpLinks {
			if dpLink.Id() == link && neigh.Id == node {
				linkHealth, ok := s.PingBuf.GetAndDelete(token)
				if ok {
					health := linkHealth.Value()
					// we have a link
					//s.Log.Debug("ping update", "peer", node, "ping", time.Since(health.Time))
					err := updateRoutes(s)
					if err != nil {
						s.Log.Error("Error updating routes: ", err)
					}
					dpLink.UpdatePing(time.Since(health.Time))
				}
				return
			}
		}
	}
	s.Log.Warn("probe came back and couldn't find link", "id", link, "node", node)
	return
}

func probeDataPlane(s *state.State) error {
	r := Get[*Router](s)
	d := Get[*DpLinkMgr](s)

	// probe existing links
	for _, neigh := range r.Neighbours {
		for _, dpLink := range neigh.DpLinks {
			go func() {
				err := probe(s.Env, d.udpSock, *dpLink.Endpoint().ProbeAddr, dpLink.Id())
				if err != nil {
					s.Log.Debug("probe failed", "err", err.Error())
				}
			}()
		}
	}

	// probe for new dp links
	for _, peer := range s.GetPeers() {
		cfg, err := s.GetPubNodeCfg(peer)
		if err != nil {
			continue
		}
		nIdx := slices.IndexFunc(r.Neighbours, func(neighbour *state.Neighbour) bool {
			return neighbour.Id == peer
		})
		if nIdx == -1 {
			continue
		}
		neigh := r.Neighbours[nIdx]
		// assumption: we don't need to connect to the same endpoint again within the scope of the same node
		for _, ep := range cfg.DpAddr {
			if slices.IndexFunc(neigh.DpLinks, func(link state.DpLink) bool {
				return !link.IsRemote() && link.Endpoint().Name == ep.Name
			}) == -1 {
				// add the link to the neighbour
				id := uuid.New()
				neigh.DpLinks = append(neigh.DpLinks, NewUdpDpLink(id, INF, ep))
				go func() {
					err := probe(s.Env, d.udpSock, *ep.ProbeAddr, id)
					if err != nil {
						//s.Log.Debug("discovery probe failed", "err", err.Error())
					}
				}()
			}
		}
	}
	return nil
}
