package mock

import (
	"github.com/google/uuid"
	"time"
)

type MockLink struct {
	VId         uuid.UUID
	VMetric     *uint16
	LastChanged time.Time
}

func (m MockLink) Id() uuid.UUID {
	return m.VId
}

func (m MockLink) Metric() uint16 {
	return *m.VMetric
}
