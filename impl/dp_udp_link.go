package impl

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/device"
	"github.com/google/uuid"
	"github.com/jellydator/ttlcache/v3"
	"github.com/rosshemsley/kalman"
	"github.com/rosshemsley/kalman/models"
	"google.golang.org/protobuf/proto"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"os"
	"slices"
	"sort"
	"strconv"
	"time"
)

type UdpDpLink struct {
	id            uuid.UUID
	metric        uint16
	metricRange   uint16
	realLatency   time.Duration
	history       []time.Duration
	boxMedian     time.Duration
	lastHeardBack time.Time
	endpoint      state.DpEndpoint
	filter        *kalman.KalmanFilter
	model         *models.SimpleModel
}

func (u *UdpDpLink) MetricRange() uint16 {
	return u.metricRange
}

func (u *UdpDpLink) Renew(remote bool) {
	if remote {
		u.lastHeardBack = time.Now()
	}
}

func (u *UdpDpLink) IsAlive() bool {
	return time.Now().Sub(u.lastHeardBack) <= LinkDeadThreshold || u.lastHeardBack.IsZero()
}

func NewUdpDpLink(id uuid.UUID, metric uint16, endpoint state.DpEndpoint) *UdpDpLink {
	// TODO: These parameters are sort of arbitrary... Probably tune them better?
	model := models.NewSimpleModel(time.Now(), float64(time.Millisecond*50), models.SimpleModelConfig{
		InitialVariance:     0,
		ProcessVariance:     float64(time.Millisecond * 10),
		ObservationVariance: float64(time.Millisecond * 10),
	})
	return &UdpDpLink{
		id:          id,
		metric:      metric,
		metricRange: 5000,
		endpoint:    endpoint,
		filter:      kalman.NewKalmanFilter(model),
		model:       model,
		boxMedian:   time.Millisecond * 1000, // start with a relatively high latency so we don't disrupt existing connections before we are sure
	}
}

func (u *UdpDpLink) Endpoint() *state.DpEndpoint {
	return &u.endpoint
}

func (u *UdpDpLink) computeRange() time.Duration {
	tmp := make([]time.Duration, len(u.history))
	copy(tmp, u.history)
	sort.Slice(tmp, func(i, j int) bool {
		return tmp[i] < tmp[j]
	})
	// median := tmp[len(tmp)/2]
	top := tmp[int(float64(len(tmp))*(1-OutlierPercentage))]
	bottom := tmp[int(float64(len(tmp))*OutlierPercentage)]
	return bottom - top
}

func (u *UdpDpLink) UpdatePing(ping time.Duration) {
	err := u.filter.Update(time.Now(), u.model.NewMeasurement(float64(ping)))
	if err != nil {
		return
	}

	// TODO: We don't have numbers of actual packets being lost.

	u.realLatency = ping
	filtered := time.Duration(u.model.Value(u.filter.State()))

	// not sure if this is a great algorithm, but it is one...
	// We determine a window based on Range
	// outliers will be dealt separately
	// When the latency gets updated, the box will be moved up or down so that it fits the new datapoint.
	// We will use the median of the box as the latency

	// tldr; if the ping fluctuates within +/- 1.5*Range, we don't change it. note, if the ping is very stable, Range will decrease too!

	u.history = append(u.history, u.realLatency)
	if len(u.history) > WindowSamples {
		u.history = u.history[1:] // discard
	}
	metRan := time.Millisecond * 5000 // default
	if len(u.history) > MinimumConfidenceWindow {
		metRan = u.computeRange()
	}
	// check if ping is within box
	bLen := time.Duration(float64(metRan) * 1.5)
	if u.boxMedian+bLen < filtered {
		// box is too low
		u.boxMedian = filtered - bLen
	} else if u.boxMedian-bLen > filtered {
		// box is too high
		u.boxMedian = filtered + bLen
	}

	if state.DBG_write_metric_history {
		writeHeader := false
		fname := fmt.Sprintf("log/latlog-%s.csv", u.Endpoint().Name)
		if _, err := os.Stat(fname); errors.Is(err, os.ErrNotExist) {
			writeHeader = true
		}
		of, err := os.OpenFile(fname, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0777)
		if err != nil {
			slog.Error("error opening file", "err", err)
		}
		w := csv.NewWriter(of)
		if writeHeader {
			w.Write([]string{"time", "real", "filtered", "windowed"})
		}
		err = w.Write([]string{
			fmt.Sprintf("%s", time.Now().String()),
			strconv.FormatInt(ping.Microseconds(), 10),
			strconv.FormatInt(filtered.Microseconds(), 10),
			strconv.FormatInt(u.boxMedian.Microseconds(), 10),
		})
		if err != nil {
			slog.Error("error writing file", "err", err)
		}
		w.Flush()
	}

	// latency in increments of 100 microseconds
	latencyContrib := u.boxMedian.Microseconds() / 100

	u.metric = uint16(min(max(latencyContrib, 1), int64(INF-1)))
	u.metric = uint16(min(max(int64(u.metric), 1), int64(INF-1)))

	u.metricRange = uint16(min(max(metRan.Microseconds()/100, 1), int64(INF-1)))

	//slog.Info("lu", "r", u.realLatency, "f", time.Duration(filtered))
}

func (u *UdpDpLink) Id() uuid.UUID {
	return u.id
}

func (u *UdpDpLink) Metric() uint16 {
	// if link is dead, return INF
	if !u.IsAlive() {
		return INF
	}
	return u.metric
}

func (u *UdpDpLink) IsRemote() bool {
	return u.endpoint.RemoteInit
}

// region probe io
func generateAnonHash(token uint64, pubKey state.NyPublicKey) []byte {
	hash := sha256.Sum256(binary.LittleEndian.AppendUint64(pubKey, token))
	return hash[:]
}

func probe(e *state.Env, sock *device.PolySock, addr netip.AddrPort, linkId uuid.UUID) error {
	if state.DBG_log_probe {
		e.Log.Debug("probe", "addr", addr, "linkId", linkId)
	}
	token := rand.Uint64()
	uid, err := linkId.MarshalBinary()
	if err != nil {
		return err
	}
	ping := &protocol.Probe{
		Token:         token,
		ResponseToken: nil,
		NodeId:        generateAnonHash(token, e.Key.XPubkey()),
		LinkId:        uid,
	}
	marshal, err := proto.Marshal(ping)
	if err != nil {
		return err
	}
	sock.Send(marshal, &conn.StdNetEndpoint{AddrPort: addr})
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
				dpLink.Renew(true)

				// refresh endpoint too, in case of roaming
				if dpLink.IsRemote() {
					dpLink.Endpoint().Addr = endpoint.Addr
				}
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

func handleProbePong(s *state.State, link uuid.UUID, node state.Node, token uint64, receptionTime time.Time, ep conn.Endpoint) {
	// check if link exists
	r := Get[*Router](s)
	for _, neigh := range r.Neighbours {
		for _, dpLink := range neigh.DpLinks {
			if dpLink.Id() == link && neigh.Id == node {
				linkHealth, ok := s.PingBuf.GetAndDelete(token)
				if ok {
					health := linkHealth.Value()
					singlePing := receptionTime.Sub(health.Time)
					// we have a link
					if state.DBG_log_probe {
						s.Log.Debug("ping update", "peer", node, "ping", singlePing)
					}
					err := updateRoutes(s)
					if err != nil {
						s.Log.Error("Error updating routes: ", err)
					}
					dpLink.UpdatePing(singlePing)
					dpLink.Renew(true)

					// refresh endpoint too, in case of roaming
					if dpLink.IsRemote() {
						dpLink.Endpoint().Addr = netip.MustParseAddrPort(ep.DstToString())
					}
				}
				return
			}
		}
	}
	s.Log.Warn("probe came back and couldn't find link", "id", link, "node", node)
	return
}

func probeExisting(s *state.State) error {
	r := Get[*Router](s)
	d := Get[*DpLinkMgr](s)

	// probe existing links
	for _, neigh := range r.Neighbours {
		for _, dpLink := range neigh.DpLinks {
			dpLink.Renew(false)
			go func() {
				err := probe(s.Env, d.polySock, dpLink.Endpoint().Addr, dpLink.Id())
				if err != nil {
					s.Log.Debug("probe failed", "err", err.Error())
				}
			}()
		}
	}
	return nil
}

func probeNew(s *state.State) error {
	r := Get[*Router](s)
	d := Get[*DpLinkMgr](s)

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
			if !ep.Addr.IsValid() {
				continue
			}
			idx := slices.IndexFunc(neigh.DpLinks, func(link state.DpLink) bool {
				return !link.IsRemote() && link.Endpoint().Name == ep.Name
			})
			if idx == -1 {
				// add the link to the neighbour
				id := uuid.New()
				neigh.DpLinks = append(neigh.DpLinks, NewUdpDpLink(id, INF, ep))
				go func() {
					err := probe(s.Env, d.polySock, ep.Addr, id)
					if err != nil {
						//s.Log.Debug("discovery probe failed", "err", err.Error())
					}
				}()
			}
		}
	}
	return nil
}
