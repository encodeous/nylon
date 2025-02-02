package impl

import "time"

const (
	INF              = (uint16)(65535)
	ProbeCtlDelay    = time.Second * 5
	RouteUpdateDelay = time.Second * 5
	ProbeDpDelay     = time.Millisecond * 1000
	StarvationDelay  = time.Millisecond * 400
)
