package state

import (
	"encoding/csv"
	"errors"
	"fmt"
	"github.com/encodeous/polyamide/conn"
	"github.com/rosshemsley/kalman"
	"github.com/rosshemsley/kalman/models"
	"log/slog"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"time"
)

type DynamicEndpoint struct {
	node          Node
	metric        uint16
	metricRange   uint16
	realLatency   time.Duration
	history       []time.Duration
	boxMedian     time.Duration
	lastHeardBack time.Time
	endpoint      *NetworkEndpoint
	filter        *kalman.KalmanFilter
	model         *models.SimpleModel
}

func (u *DynamicEndpoint) Node() Node {
	return u.node
}

func (u *DynamicEndpoint) IsActive() bool {
	return time.Now().Sub(u.lastHeardBack) <= LinkDeadThreshold
}

func (u *DynamicEndpoint) MetricRange() uint16 {
	return u.metricRange
}

func (u *DynamicEndpoint) Renew() {
	u.lastHeardBack = time.Now()
}

func (u *DynamicEndpoint) IsAlive() bool {
	return u.IsActive() || !u.endpoint.RemoteInit // we never gc endpoints that we have in our config
}

func NewEndpoint(endpoint netip.AddrPort, node Node, remoteInit bool, wgEndpoint conn.Endpoint) *DynamicEndpoint {
	// TODO: These parameters are sort of arbitrary... Probably tune them better?
	model := models.NewSimpleModel(time.Now(), float64(time.Millisecond*50), models.SimpleModelConfig{
		InitialVariance:     0,
		ProcessVariance:     float64(time.Millisecond * 10),
		ObservationVariance: float64(time.Millisecond * 10),
	})
	return &DynamicEndpoint{
		metric:      INF,
		metricRange: 5000,
		endpoint: &NetworkEndpoint{
			RemoteInit: remoteInit,
			WgEndpoint: wgEndpoint,
			Ep:         endpoint,
		},
		filter:    kalman.NewKalmanFilter(model),
		model:     model,
		node:      node,
		boxMedian: time.Millisecond * 1000, // start with a relatively high latency so we don't disrupt existing connections before we are sure
	}
}

func (u *DynamicEndpoint) NetworkEndpoint() *NetworkEndpoint {
	return u.endpoint
}

func (u *DynamicEndpoint) computeRange() time.Duration {
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

func (u *DynamicEndpoint) UpdatePing(ping time.Duration) {
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

	if DBG_write_metric_history {
		writeHeader := false
		fname := fmt.Sprintf("log/latlog-%s.csv", u.NetworkEndpoint())
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

func (u *DynamicEndpoint) Metric() uint16 {
	// if link is dead, return INF
	if !u.IsActive() {
		return INF
	}
	return u.metric
}

func (u *DynamicEndpoint) IsRemote() bool {
	return u.endpoint.RemoteInit
}
