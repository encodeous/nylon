package udp_link

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
	"slices"
	"time"
)

type DpLinkMgr struct {
	client     *wgctrl.Client
	deviceName string
}

type linkPing struct {
	LinkId uuid.UUID
	Node   state.Node
	Time   time.Time
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

func handleProbeComplete(s *state.State, link uuid.UUID, node state.Node, elapsed time.Duration) error {
	// check if link exists, otherwise create a dplink
}

func probeDataPlane(e *state.Env) {
	for e.Context.Err() == nil {
		for _, neigh := range e.GetPeers() {

		}
		time.Sleep(ProbeDpDelay)
	}
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

	go probeListener(s.Env)
	go probeDataPlane(s.Env)
	return nil
}

// region probe io
func generateAnonHash(token uint64, pubKey state.EdPublicKey) []byte {
	hash := sha256.Sum256(binary.LittleEndian.AppendUint64(pubKey, token))
	return hash[:]
}

func probeListener(e *state.Env) {
	listener, err := net.ListenUDP("udp", &net.UDPAddr{IP: nil, Port: int(e.ProbeAddr.Port())})

	e.Log.Info("started probe listener")

	if err != nil {
		e.Cancel(err)
	}
	defer listener.Close()
	latencyMap := ttlcache.New[uint64, linkPing](
		ttlcache.WithTTL[uint64, linkPing](5*time.Second),
		ttlcache.WithDisableTouchOnHit[uint64, linkPing](),
	)
	go latencyMap.Start()
	defer latencyMap.Stop()
	for e.Context.Err() == nil {
		buf := make([]byte, 1024)
		n, addrport, err := listener.ReadFromUDPAddrPort(buf)
		if err != nil {
			continue
		}
		go func() {
			req := &protocol.ProbePing{}
			err := proto.Unmarshal(buf[:n], req)
			if err != nil {
				return
			}
			if req.Finalize {
				// special code to handle case where one end doesn't have a public port
				if latencyMap.Has(req.Token) {
					item := latencyMap.Get(req.Token).Value()
					elapsed := time.Since(item.Time)
					e.Dispatch(func(s *state.State) error {
						return handleProbeComplete(s, item.LinkId, item.Node, elapsed)
					})
				}
			} else {
				for _, node := range e.Nodes {
					if node.Id != e.Id && slices.Equal(generateAnonHash(req.Token, node.PubKey), req.NodeId) {
						token := rand.Uint64()
						uid, err := uuid.Parse(req.LinkId)
						if err != nil {
							return
						}
						res := &protocol.ProbePong{
							Token:         req.Token,
							ResponseToken: token,
							NodeId:        generateAnonHash(token, e.Key.Pubkey()),
							LinkId:        req.LinkId,
						}
						pktBytes, err := proto.Marshal(res)
						if err != nil {
							return
						}
						// TODO: Remove after debugging
						time.Sleep(time.Duration((int64)(time.Millisecond) * 10 * (int64)(mock.GetMinMockWeight(e.Id, node.Id, e.CentralCfg))))

						listener.WriteToUDPAddrPort(pktBytes, addrport)
						latencyMap.Set(token, linkPing{
							LinkId: uid,
							Node:   state.Node(req.NodeId),
							Time:   time.Now(),
						}, ttlcache.DefaultTTL)
						return
					}
				}
			}
		}()
	}
}

func probe(e *state.Env, addr *net.UDPAddr, peer state.PubNodeCfg, linkId uuid.UUID) error {
	udp, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return err
	}
	err = udp.SetReadDeadline(time.Now().Add(time.Second * 1))
	if err != nil {
		return err
	}
	defer udp.Close()
	token := rand.Uint64()

	// send out ping
	ping := &protocol.ProbePing{
		Token:  token,
		NodeId: generateAnonHash(token, e.Key.Pubkey()),
		LinkId: linkId.String(),
	}
	marshal, err := proto.Marshal(ping)
	if err != nil {
		return err
	}
	start := time.Now()
	_, err = udp.Write(marshal)
	if err != nil {
		return err
	}

	// get pong
	response := &protocol.ProbePong{}
	buf := make([]byte, 128)
	n, err := udp.Read(buf)
	if err != nil {
		return err
	}
	elapsed := time.Since(start)
	err = proto.Unmarshal(buf[:n], response)
	if err != nil {
		return err
	}
	if response.Token != token || !slices.Equal(generateAnonHash(response.ResponseToken, peer.PubKey), response.NodeId) {
		return nil
	}

	// send finalizer
	ping = &protocol.ProbePing{
		Token:    response.ResponseToken,
		Finalize: true,
	}
	marshal, err = proto.Marshal(ping)
	if err != nil {
		return err
	}
	_, err = udp.Write(marshal)
	if err != nil {
		return err
	}
	e.Dispatch(func(s *state.State) error {
		return handleProbeComplete(s, linkId, peer.Id, elapsed)
	})
	return nil
}

// endregion probe io
