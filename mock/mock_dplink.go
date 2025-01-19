package mock

import "github.com/google/uuid"

type MockLink struct {
	VId     uuid.UUID
	VMetric uint16
}

func (m MockLink) Id() uuid.UUID {
	return m.VId
}

func (m MockLink) Metric() uint16 {
	return m.VMetric
}
