package impl

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"github.com/encodeous/nylon/dp_wireguard"
	"github.com/encodeous/nylon/mock"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/protobuf/proto"
	"math/rand/v2"
	"net"
	"net/netip"
	"slices"
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
			Endpoint:     nil,
			AllowedIPs:   nil,
		})
	}

	portAddr := int(s.DpBind.Port())
	cfg := wgtypes.Config{
		ListenPort:   &portAddr,
		ReplacePeers: true,
		Peers:        peers,
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
	return nil
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
						// TODO: Remove after debugging
						weight := mock.GetMinMockWeight(e.Id, node.Id, e.CentralCfg)
						if weight == 0 {
							return
						}
						time.Sleep(weight)

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
