package udp_link

import "time"

const (
	INF              = (uint16)(65535)
	ProbeCtlDelay    = time.Second * 5
	RouteUpdateDelay = time.Millisecond * 5000
	ProbeDpDelay     = time.Millisecond * 400
)
