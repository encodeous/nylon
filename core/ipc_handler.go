package core

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/encodeous/nylon/ipc"
	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

const (
	defaultIPCProbeTimeout = 2 * time.Second
	maxIPCProbeTimeout     = 10 * time.Second
	// maxIPCInFlight bounds the requests one connection can have running.
	maxIPCInFlight = 16
)

// ipcServer accepts connections on nylon's socket.
type ipcServer struct {
	listener net.Listener
	workers  sync.WaitGroup
}

// serve accepts connections until the listener closes.
func (s *ipcServer) serve(n *Nylon) {
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		for n.Context.Err() == nil {
			c, err := s.listener.Accept()
			if err != nil {
				if n.Context.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				n.Log.Debug("IPC accept failed", "err", err)
				continue
			}
			s.workers.Add(1)
			go func() {
				defer s.workers.Done()
				if err := ServeIPC(n, c); err != nil {
					n.Log.Debug("IPC connection ended", "err", err)
				}
			}()
		}
	}()
}

func (s *ipcServer) close() error {
	err := s.listener.Close()
	s.workers.Wait()
	return err
}

// ServeIPC answers requests on conn until the client hangs up or nylon stops. It closes conn.
func ServeIPC(n *Nylon, conn net.Conn) error {
	defer conn.Close()
	stop := context.AfterFunc(n.Context, func() { _ = conn.Close() })
	defer stop()

	r := bufio.NewReader(conn)
	version, err := ipc.ReadHello(r)
	if err != nil {
		return fmt.Errorf("read hello: %w", err)
	}
	// Answer either way, so a client with another version can say which.
	if err := ipc.WriteHello(conn); err != nil {
		return err
	}
	if version != ipc.Version {
		return ipc.ErrVersion{Local: ipc.Version, Remote: version}
	}

	ctx, cancel := context.WithCancel(n.Context)
	s := &ipcSession{n: n, conn: conn, active: make(map[uint32]context.CancelFunc)}
	defer func() {
		cancel()
		_ = conn.Close() // unblocks a request stuck writing to a client that stopped reading
		s.requests.Wait()
	}()
	for {
		req := &protocol.IpcRequest{}
		if err := ipc.ReadFrame(r, req); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return nil
			}
			// The stream can't be trusted after a bad frame, so say why and hang up.
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_ = s.send(&protocol.IpcResponse{Error: ipc.Errorf(protocol.IpcError_INVALID_REQUEST, "bad request: %v", err)})
			return err
		}
		if c := req.GetCancel(); c != nil {
			s.cancel(c.Id)
			continue
		}
		s.start(ctx, req)
	}
}

// ipcSession is one client connection. Its requests run concurrently.
type ipcSession struct {
	n        *Nylon
	conn     net.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	active   map[uint32]context.CancelFunc
	requests sync.WaitGroup
}

func (s *ipcSession) send(resp *protocol.IpcResponse) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return ipc.WriteFrame(s.conn, resp)
}

func (s *ipcSession) start(ctx context.Context, req *protocol.IpcRequest) {
	id := req.Id
	s.mu.Lock()
	_, dup := s.active[id]
	busy := len(s.active) >= maxIPCInFlight
	var cancel context.CancelFunc
	if !dup && !busy {
		ctx, cancel = context.WithCancel(ctx)
		s.active[id] = cancel
	}
	s.mu.Unlock()
	switch {
	case dup:
		_ = s.send(&protocol.IpcResponse{Id: id, Error: ipc.Errorf(protocol.IpcError_INVALID_REQUEST, "request %d is already running", id)})
		return
	case busy:
		_ = s.send(&protocol.IpcResponse{Id: id, Error: ipc.Errorf(protocol.IpcError_UNAVAILABLE, "too many requests in flight")})
		return
	}
	s.requests.Add(1)
	go func() {
		defer s.requests.Done()
		defer s.cancel(id)
		handleIPCRequest(ctx, s.n, req, func(resp *protocol.IpcResponse) error {
			resp.Id = id
			return s.send(resp)
		})
	}()
}

func (s *ipcSession) cancel(id uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cancel, ok := s.active[id]; ok {
		cancel()
		delete(s.active, id)
	}
}

func errResponse(code protocol.IpcError_Code, format string, args ...any) *protocol.IpcResponse {
	return &protocol.IpcResponse{Error: ipc.Errorf(code, format, args...)}
}

// handleIPCRequest answers req through send, which a trace calls once per event.
func handleIPCRequest(ctx context.Context, n *Nylon, req *protocol.IpcRequest, send func(*protocol.IpcResponse) error) {
	var resp *protocol.IpcResponse
	switch req.Request.(type) {
	case *protocol.IpcRequest_Status:
		resp = dispatchIPC(ctx, n, func() *protocol.IpcResponse { return handleStatus(n, req.GetStatus()) })
	case *protocol.IpcRequest_Reload:
		resp = dispatchIPC(ctx, n, func() *protocol.IpcResponse { return handleIPCReload(n, req.GetReload()) })
	case *protocol.IpcRequest_Probe:
		resp = handleIPCProbe(ctx, n, req.GetProbe())
	case *protocol.IpcRequest_Trace:
		handleTrace(ctx, n, send)
		return
	default:
		resp = errResponse(protocol.IpcError_INVALID_REQUEST, "unknown request")
	}
	_ = send(resp)
}

// dispatchIPC runs fn on the dispatcher, which owns nylon's state.
func dispatchIPC(ctx context.Context, n *Nylon, fn func() *protocol.IpcResponse) *protocol.IpcResponse {
	done := make(chan *protocol.IpcResponse, 1)
	n.Dispatch(func() error {
		done <- fn()
		return nil
	})
	select {
	case resp := <-done:
		return resp
	case <-ctx.Done():
		return errResponse(protocol.IpcError_UNAVAILABLE, "nylon shutting down")
	case <-time.After(n.IPCDispatchTimeout):
		// nylon is too busy to handle IPC requests
		return errResponse(protocol.IpcError_UNAVAILABLE, "timed out waiting for dispatch")
	}
}

func handleStatus(n *Nylon, req *protocol.StatusRequest) *protocol.IpcResponse {
	activeEps := 0
	for _, neigh := range n.RouterState.Neighbours {
		for _, ep := range neigh.Eps {
			if ep.IsActive() {
				activeEps++
			}
		}
	}

	txBytes := uint64(0)
	rxBytes := uint64(0)
	peerStats := transportPeerStats(n)
	for _, stat := range peerStats {
		txBytes += stat.TxBytes
		rxBytes += stat.RxBytes
	}

	listenPort := uint32(n.LocalCfg.Port)
	// A transport that binds a port knows which once it starts.
	if t, ok := n.defaultTransport().(interface{ ListenPort() uint16 }); ok {
		if port := t.ListenPort(); port != 0 {
			listenPort = uint32(port)
		}
	}

	return &protocol.IpcResponse{
		Response: &protocol.IpcResponse_Status{Status: &protocol.StatusResponse{
			Node: &protocol.NodeStatus{
				NodeId:          string(n.LocalCfg.Id),
				Interface:       n.Interface,
				PublicKey:       keyString(n.LocalCfg.Key.Pubkey()),
				ListenPort:      listenPort,
				ConfigTimestamp: n.CentralCfg.Timestamp,
				TraceEnabled:    n.DBG_trace_tc,
				Advertised:      buildAdvertisements(n),
				Seqnos:          buildSeqnos(n),
				Stats: &protocol.NodeStats{
					NeighbourCount:        int32(len(n.RouterState.Neighbours)),
					ActiveEndpointCount:   int32(activeEps),
					SelectedRouteCount:    int32(len(n.RouterState.Routes)),
					AdvertisedPrefixCount: int32(len(n.RouterState.Advertised)),
					TxBytes:               txBytes,
					RxBytes:               rxBytes,
				},
			},
			Neighbours:           buildNeighbours(n, peerStats),
			Routes:               buildRouteTables(n),
			FeasibilityDistances: buildFeasibilityDistances(n),
		}},
	}
}

func buildAdvertisements(n *Nylon) []*protocol.Advertisement {
	entries := make([]*protocol.Advertisement, 0, len(n.RouterState.Advertised))
	for prefix, adv := range n.RouterState.Advertised {
		entries = append(entries, &protocol.Advertisement{
			Prefix:      prefix.String(),
			NodeId:      string(adv.NodeId),
			Metric:      adv.MetricFn(),
			ExpiryUnix:  adv.Expiry.Unix(),
			PassiveHold: adv.IsPassiveHold,
		})
	}
	sortAdvertisements(entries)
	return entries
}

func buildSeqnos(n *Nylon) []*protocol.SeqnoEntry {
	entries := make([]*protocol.SeqnoEntry, 0, len(n.RouterState.SelfSeqno))
	for prefix, seqno := range n.RouterState.SelfSeqno {
		entries = append(entries, &protocol.SeqnoEntry{
			Prefix: prefix.String(),
			Seqno:  uint32(seqno),
		})
	}
	slices.SortFunc(entries, func(a, b *protocol.SeqnoEntry) int {
		return cmp.Compare(a.Prefix, b.Prefix)
	})
	return entries
}

func buildNeighbours(n *Nylon, peerStats map[state.NyPublicKey]polyamide.PeerStats) []*protocol.NeighbourInfo {
	ids := slices.Clone(n.GetPeers(n.LocalCfg.Id))
	slices.Sort(ids)
	neighbours := make([]*protocol.NeighbourInfo, 0, len(ids))
	for _, id := range ids {
		cfg := n.GetNode(id)
		neigh := n.RouterState.GetNeighbour(id)
		eps := make([]*protocol.EndpointInfo, 0)
		routes := make([]*protocol.NeighRoute, 0)
		if neigh != nil {
			eps = n.buildEndpoints(neigh)
			routes = buildNeighRoutes(neigh)
		}
		stat := peerStats[cfg.PubKey]
		neighbours = append(neighbours, &protocol.NeighbourInfo{
			PeerId:        string(id),
			PublicKey:     keyString(cfg.PubKey),
			PassiveClient: n.IsClient(id),
			Endpoints:     eps,
			Routes:        routes,
			Advertised:    advertisementsForNode(n, id),
			Transport:     transportPeerStatsProto(stat),
		})
	}
	return neighbours
}

func (n *Nylon) buildEndpoints(neigh *state.Neighbour) []*protocol.EndpointInfo {
	eps := make([]*protocol.EndpointInfo, 0, len(neigh.Eps))
	for _, ep := range neigh.Eps {
		nep := ep.AsNylonEndpoint()
		var resolved *string
		if ap, err := n.EndpointResolver.Get(nep.Address); err == nil {
			resolved = new(ap.String())
		}
		eps = append(eps, &protocol.EndpointInfo{
			Address:         nep.Address,
			Resolved:        resolved,
			Active:          ep.IsActive(),
			RemoteInit:      ep.IsRemote(),
			Metric:          ep.Metric(),
			FilteredRttNs:   int64(nep.FilteredPing()),
			StabilizedRttNs: int64(nep.StabilizedPing()),
		})
	}
	slices.SortFunc(eps, func(a, b *protocol.EndpointInfo) int {
		if cmpMetric := cmp.Compare(a.Metric, b.Metric); cmpMetric != 0 {
			return cmpMetric
		}
		return cmp.Compare(a.Address, b.Address)
	})
	return eps
}

func buildNeighRoutes(neigh *state.Neighbour) []*protocol.NeighRoute {
	routes := make([]*protocol.NeighRoute, 0, len(neigh.Routes))
	for _, route := range neigh.Routes {
		routes = append(routes, neighRouteProto(route))
	}
	slices.SortFunc(routes, func(a, b *protocol.NeighRoute) int {
		return comparePubRoute(a.PubRoute, b.PubRoute)
	})
	return routes
}

func buildRouteTables(n *Nylon) *protocol.RouteTables {
	tables := &protocol.RouteTables{}
	forwarding := n.router.Tables.Load()
	for _, route := range n.RouterState.Routes {
		tables.Selected = append(tables.Selected, selRouteProto(route))
	}
	slices.SortFunc(tables.Selected, func(a, b *protocol.SelRoute) int {
		return comparePubRoute(a.PubRoute, b.PubRoute)
	})
	for prefix, route := range forwarding.Forward.All() {
		tables.Forward = append(tables.Forward, &protocol.RouteTableEntry{
			Prefix:    prefix.String(),
			Nh:        string(route.Nh),
			Blackhole: route.Blackhole,
		})
	}
	sortRouteTableEntries(tables.Forward)
	for prefix, route := range forwarding.Exit.All() {
		tables.Exit = append(tables.Exit, &protocol.RouteTableEntry{
			Prefix:    prefix.String(),
			Nh:        string(route.Nh),
			Blackhole: route.Blackhole,
		})
	}
	sortRouteTableEntries(tables.Exit)
	return tables
}

func buildFeasibilityDistances(n *Nylon) []*protocol.FeasibilityDistance {
	entries := make([]*protocol.FeasibilityDistance, 0, len(n.RouterState.Sources))
	for source, fd := range n.RouterState.Sources {
		entries = append(entries, &protocol.FeasibilityDistance{
			Source: sourceProto(source),
			Fd:     fdProto(fd),
		})
	}
	slices.SortFunc(entries, func(a, b *protocol.FeasibilityDistance) int {
		if c := cmp.Compare(a.Source.Prefix, b.Source.Prefix); c != 0 {
			return c
		}
		return cmp.Compare(a.Source.NodeId, b.Source.NodeId)
	})
	return entries
}

func advertisementsForNode(n *Nylon, id state.NodeId) []*protocol.Advertisement {
	ads := make([]*protocol.Advertisement, 0)
	for prefix, adv := range n.RouterState.Advertised {
		if adv.NodeId != id {
			continue
		}
		ads = append(ads, &protocol.Advertisement{
			NodeId:      string(adv.NodeId),
			Prefix:      prefix.String(),
			Metric:      adv.MetricFn(),
			ExpiryUnix:  adv.Expiry.Unix(),
			PassiveHold: adv.IsPassiveHold,
		})
	}
	sortAdvertisements(ads)
	return ads
}

func transportPeerStats(n *Nylon) map[state.NyPublicKey]polyamide.PeerStats {
	stats := make(map[state.NyPublicKey]polyamide.PeerStats)
	if len(n.Transports) == 0 {
		return stats
	}
	for _, stat := range n.transportPeers() {
		if key, ok := peerPublicKey(stat.Peer); ok {
			stats[key] = stat
		}
	}
	return stats
}
func transportPeerStatsProto(stat polyamide.PeerStats) *protocol.TransportPeerStats {
	var handshake int64
	if !stat.LastHandshake.IsZero() {
		handshake = stat.LastHandshake.UnixNano()
	}
	endpoint := ""
	if stat.PreferredEndpoint != nil {
		endpoint = stat.PreferredEndpoint.Address()
	}
	return &protocol.TransportPeerStats{LatestHandshakeUnix: handshake, TxBytes: stat.TxBytes, RxBytes: stat.RxBytes, PersistentKeepaliveInterval: uint32(stat.Keepalive / time.Second), Endpoint: &endpoint}
}

func sourceProto(source state.Source) *protocol.Source {
	return &protocol.Source{
		NodeId: string(source.NodeId),
		Prefix: source.Prefix.String(),
	}
}

func fdProto(fd state.FD) *protocol.FD {
	return &protocol.FD{
		Seqno:  uint32(fd.Seqno),
		Metric: fd.Metric,
	}
}

func pubRouteProto(route state.PubRoute) *protocol.PubRoute {
	return &protocol.PubRoute{
		Source: sourceProto(route.Source),
		Fd:     fdProto(route.FD),
	}
}

func neighRouteProto(route state.NeighRoute) *protocol.NeighRoute {
	return &protocol.NeighRoute{
		PubRoute:     pubRouteProto(route.PubRoute),
		ExpireAtUnix: route.ExpireAt.Unix(),
	}
}

func selRouteProto(route state.SelRoute) *protocol.SelRoute {
	retractedBy := make([]string, 0, len(route.RetractedBy))
	for _, id := range route.RetractedBy {
		retractedBy = append(retractedBy, string(id))
	}
	slices.Sort(retractedBy)
	return &protocol.SelRoute{
		PubRoute:     pubRouteProto(route.PubRoute),
		Nh:           string(route.Nh),
		ExpireAtUnix: route.ExpireAt.Unix(),
		RetractedBy:  retractedBy,
	}
}

func keyString(key state.NyPublicKey) string {
	text, err := key.MarshalText()
	if err != nil {
		return ""
	}
	return string(text)
}

func comparePubRoute(a, b *protocol.PubRoute) int {
	if c := cmp.Compare(a.Source.Prefix, b.Source.Prefix); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Source.NodeId, b.Source.NodeId); c != 0 {
		return c
	}
	if c := cmp.Compare(a.Fd.Seqno, b.Fd.Seqno); c != 0 {
		return c
	}
	return cmp.Compare(a.Fd.Metric, b.Fd.Metric)
}

func sortAdvertisements(entries []*protocol.Advertisement) {
	slices.SortFunc(entries, func(a, b *protocol.Advertisement) int {
		if c := cmp.Compare(a.Prefix, b.Prefix); c != 0 {
			return c
		}
		return cmp.Compare(a.NodeId, b.NodeId)
	})
}

func sortRouteTableEntries(entries []*protocol.RouteTableEntry) {
	slices.SortFunc(entries, func(a, b *protocol.RouteTableEntry) int {
		if c := cmp.Compare(a.Prefix, b.Prefix); c != 0 {
			return c
		}
		return cmp.Compare(a.Nh, b.Nh)
	})
}

func handleIPCProbe(ctx context.Context, n *Nylon, req *protocol.ProbeRequest) *protocol.IpcResponse {
	probeTimeout := ipcProbeTimeout(req)

	dispatchCtx, dispatchCancel := context.WithTimeout(ctx, n.IPCDispatchTimeout)
	defer dispatchCancel()
	probes, err := NewDispatchFuture(n, func() ([]Future[*protocol.EndpointProbeResult], error) {
		return n.sendEndpointProbes(state.NodeId(req.PeerId), probeTimeout)
	}).Await(dispatchCtx)
	if err != nil {
		if ctxErr := dispatchCtx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return errResponse(protocol.IpcError_UNAVAILABLE, "timed out waiting for dispatch")
		}
		// the only other failure: the peer isn't a neighbour
		return errResponse(protocol.IpcError_NOT_FOUND, "%v", err)
	}

	probeCtx, probeCancel := context.WithTimeout(ctx, probeTimeout+n.IPCDispatchTimeout)
	defer probeCancel()
	results, err := AwaitAll(probeCtx, probes)
	if err != nil {
		if ctxErr := probeCtx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			return errResponse(protocol.IpcError_UNAVAILABLE, "%v", err)
		}
	}
	return &protocol.IpcResponse{
		Response: &protocol.IpcResponse_Probe{Probe: &protocol.ProbeResponse{Results: results}},
	}
}

func ipcProbeTimeout(req *protocol.ProbeRequest) time.Duration {
	if req.TimeoutMs == 0 {
		return defaultIPCProbeTimeout
	}
	return min(time.Duration(req.TimeoutMs)*time.Millisecond, maxIPCProbeTimeout)
}

func handleIPCReload(n *Nylon, req *protocol.ReloadRequest) *protocol.IpcResponse {
	data, err := os.ReadFile(n.ConfigPath)
	if err != nil {
		return errResponse(protocol.IpcError_INTERNAL, "read file: %v", err)
	}
	var cfg state.CentralCfg
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return errResponse(protocol.IpcError_INVALID_REQUEST, "parse config: %v", err)
	}
	// We're running on the dispatch goroutine, so call ApplyCentralConfig
	// directly rather than re-dispatching (which would deadlock).
	result, err := n.ApplyCentralConfig(&cfg)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	var protoResult protocol.ReloadResult
	switch result {
	case ApplyNoop:
		protoResult = protocol.ReloadResult_NOOP
	case ApplyApplied:
		protoResult = protocol.ReloadResult_APPLIED
	case ApplyRejected:
		protoResult = protocol.ReloadResult_REJECTED
	case ApplyRestartRequired:
		protoResult = protocol.ReloadResult_RESTART_REQUIRED
	}
	resp := &protocol.IpcResponse{
		Response: &protocol.IpcResponse_Reload{Reload: &protocol.ReloadResponse{
			Result:  protoResult,
			Message: msg,
		}},
	}
	if result == ApplyRejected {
		resp.Error = ipc.Errorf(protocol.IpcError_INVALID_REQUEST, "config rejected: %s", msg)
	}
	return resp
}

// handleTrace sends each trace line until ctx ends: the client cancels, hangs up, or nylon stops.
func handleTrace(ctx context.Context, n *Nylon, send func(*protocol.IpcResponse) error) {
	if !n.DBG_trace_tc {
		_ = send(errResponse(protocol.IpcError_FAILED_PRECONDITION, "tracing not enabled; restart with --dbg-trace-tc"))
		return
	}
	ch := make(chan interface{})
	n.Trace.Register(ch)
	defer n.Trace.Unregister(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			if str, ok := msg.(string); ok {
				if err := send(&protocol.IpcResponse{Response: &protocol.IpcResponse_Trace{Trace: &protocol.TraceEvent{Line: str}}}); err != nil {
					return
				}
			}
		}
	}
}
