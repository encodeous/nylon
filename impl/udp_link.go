package impl

import (
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"github.com/rosshemsley/kalman"
	"github.com/rosshemsley/kalman/models"
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
