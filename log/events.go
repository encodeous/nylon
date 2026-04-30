package log

const (
	// Scopes
	ScopeRouter    = "router"
	ScopePolyamide = "polyamide"
)

const (
	// Router Events
	EventRouteInserted     = "route_inserted"
	EventRouteUpdated      = "route_updated"
	EventRouteRetracted    = "route_retracted"
	EventRouteExpired      = "route_expired"
	EventMajorRouteChange  = "major_route_change"
	EventInconsistentState = "inconsistent_state"
	EventNoEndpointToNeigh = "no_endpoint_to_neighbour"
	EventSeqnoRequested    = "seqno_requested"
)
