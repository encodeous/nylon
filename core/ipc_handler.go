package core

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"google.golang.org/protobuf/encoding/protojson"
)

var pjMarshal = protojson.MarshalOptions{EmitUnpopulated: true}
var pjUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

func writeResponse(rw *bufio.ReadWriter, resp *protocol.IpcResponse) error {
	data, err := pjMarshal.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	_, err = rw.Write(data)
	if err != nil {
		return err
	}
	_, err = rw.WriteString("\n")
	if err != nil {
		return err
	}
	return rw.Flush()
}

func errResponse(msg string) *protocol.IpcResponse {
	return &protocol.IpcResponse{Ok: false, Error: msg}
}

func readRequest(rw *bufio.ReadWriter) (*protocol.IpcRequest, error) {
	line, err := rw.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	req := &protocol.IpcRequest{}
	if err := pjUnmarshal.Unmarshal(line, req); err != nil {
		return nil, fmt.Errorf("unmarshal request: %w", err)
	}
	return req, nil
}

func HandleNylonIPC(n *Nylon, rw *bufio.ReadWriter) error {
	req, err := readRequest(rw)
	if err != nil {
		if err := writeResponse(rw, errResponse(err.Error())); err != nil {
			return err
		}
		return device.ErrIPCStatusHandled
	}
	var resp *protocol.IpcResponse
	switch req.Request.(type) {
	case *protocol.IpcRequest_Status:
		resp = handleStatus(n, req.GetStatus())
	case *protocol.IpcRequest_Probe:
		resp = handleIPCProbe(n, req.GetProbe())
	case *protocol.IpcRequest_Reload:
		resp = handleIPCReload(n, req.GetReload())
	case *protocol.IpcRequest_Trace:
		return handleTrace(n, rw)
	default:
		resp = errResponse("unknown method")
	}
	if err := writeResponse(rw, resp); err != nil {
		return err
	}
	return device.ErrIPCStatusHandled
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
	wgStats := wireGuardPeerStats(n)
	for _, stat := range wgStats {
		txBytes += stat.TxBytes
		rxBytes += stat.RxBytes
	}

	listenPort := uint32(n.LocalCfg.Port)
	if n.Device != nil {
		listenPort = uint32(n.Device.ListenPort())
	}

	return &protocol.IpcResponse{
		Ok: true,
		Response: &protocol.IpcResponse_Status{Status: &protocol.StatusResponse{
			Node: &protocol.NodeStatus{
				NodeId:          string(n.LocalCfg.Id),
				Interface:       n.Interface,
				PublicKey:       keyString(n.LocalCfg.Key.Pubkey()),
				ListenPort:      listenPort,
				ConfigTimestamp: n.CentralCfg.Timestamp,
				TraceEnabled:    state.DBG_trace_tc,
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
			Neighbours:           buildNeighbours(n, wgStats),
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

func buildNeighbours(n *Nylon, wgStats map[state.NyPublicKey]device.PeerStatus) []*protocol.NeighbourInfo {
	ids := slices.Clone(n.GetPeers(n.LocalCfg.Id))
	slices.Sort(ids)
	neighbours := make([]*protocol.NeighbourInfo, 0, len(ids))
	for _, id := range ids {
		cfg := n.GetNode(id)
		neigh := n.RouterState.GetNeighbour(id)
		eps := make([]*protocol.EndpointInfo, 0)
		routes := make([]*protocol.NeighRoute, 0)
		if neigh != nil {
			eps = buildEndpoints(neigh)
			routes = buildNeighRoutes(neigh)
		}
		stat := wgStats[cfg.PubKey]
		neighbours = append(neighbours, &protocol.NeighbourInfo{
			PeerId:        string(id),
			PublicKey:     keyString(cfg.PubKey),
			PassiveClient: n.IsClient(id),
			Endpoints:     eps,
			Routes:        routes,
			Advertised:    advertisementsForNode(n, id),
			Wireguard:     wireGuardPeerStatsProto(stat),
		})
	}
	return neighbours
}

func buildEndpoints(neigh *state.Neighbour) []*protocol.EndpointInfo {
	eps := make([]*protocol.EndpointInfo, 0, len(neigh.Eps))
	for _, ep := range neigh.Eps {
		nep := ep.AsNylonEndpoint()
		var resolved *string
		if ap, err := nep.DynEP.Get(); err == nil {
			resolved = stringPtr(ap.String())
		}
		eps = append(eps, &protocol.EndpointInfo{
			Address:         nep.DynEP.Value,
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
	for _, route := range n.RouterState.Routes {
		tables.Selected = append(tables.Selected, selRouteProto(route))
	}
	slices.SortFunc(tables.Selected, func(a, b *protocol.SelRoute) int {
		return comparePubRoute(a.PubRoute, b.PubRoute)
	})
	for prefix, route := range n.router.ForwardTable.All() {
		tables.Forward = append(tables.Forward, &protocol.RouteTableEntry{
			Prefix:    prefix.String(),
			Nh:        string(route.Nh),
			Blackhole: route.Blackhole,
		})
	}
	sortRouteTableEntries(tables.Forward)
	for prefix, route := range n.router.ExitTable.All() {
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

func wireGuardPeerStats(n *Nylon) map[state.NyPublicKey]device.PeerStatus {
	stats := make(map[state.NyPublicKey]device.PeerStatus)
	if n.Device == nil {
		return stats
	}
	for _, peer := range n.Device.GetPeers() {
		stat := peer.Status()
		stats[state.NyPublicKey(stat.PublicKey)] = stat
	}
	return stats
}

func wireGuardPeerStatsProto(stat device.PeerStatus) *protocol.WireGuardPeerStats {
	return &protocol.WireGuardPeerStats{
		LatestHandshakeUnix:         stat.LatestHandshakeTime().UnixNano(),
		TxBytes:                     stat.TxBytes,
		RxBytes:                     stat.RxBytes,
		PersistentKeepaliveInterval: stat.PersistentKeepaliveInterval,
		Endpoint:                    &stat.Endpoint,
	}
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

func stringPtr(v string) *string {
	return &v
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

func handleIPCProbe(n *Nylon, req *protocol.ProbeRequest) *protocol.IpcResponse {
	neigh := n.RouterState.GetNeighbour(state.NodeId(req.PeerId))
	if neigh == nil {
		return errResponse(fmt.Sprintf("peer %q is not a neighbour", req.PeerId))
	}
	results := make([]*protocol.EndpointProbeResult, 0, len(neigh.Eps))
	for _, ep := range neigh.Eps {
		nep := ep.AsNylonEndpoint()
		addr := nep.DynEP.Value
		err := n.Probe(neigh.Id, nep)
		r := &protocol.EndpointProbeResult{Address: addr, Success: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return &protocol.IpcResponse{
		Ok:       true,
		Response: &protocol.IpcResponse_Probe{Probe: &protocol.ProbeResponse{Results: results}},
	}
}

func handleIPCReload(n *Nylon, req *protocol.ReloadRequest) *protocol.IpcResponse {
	data, err := os.ReadFile(n.ConfigPath)
	if err != nil {
		return errResponse(fmt.Sprintf("read file: %v", err))
	}
	var cfg state.CentralCfg
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return errResponse(fmt.Sprintf("parse config: %v", err))
	}
	result, err := applyCentralConfigSync(n, cfg)
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
	return &protocol.IpcResponse{
		Ok: result != ApplyRejected,
		Response: &protocol.IpcResponse_Reload{Reload: &protocol.ReloadResponse{
			Result:  protoResult,
			Message: msg,
		}},
	}
}

func applyCentralConfigSync(n *Nylon, cfg state.CentralCfg) (ApplyResult, error) {
	type result struct {
		applyResult ApplyResult
		err         error
	}
	done := make(chan result, 1)
	n.Dispatch(func() error {
		applyResult, err := n.ApplyCentralConfig(cfg)
		done <- result{applyResult: applyResult, err: err}
		return nil
	})

	select {
	case r := <-done:
		return r.applyResult, r.err
	case <-n.Context.Done():
		return ApplyRejected, context.Cause(n.Context)
	case <-time.After(30 * time.Second):
		return ApplyRejected, fmt.Errorf("timed out waiting for config reload")
	}
}

func handleTrace(n *Nylon, rw *bufio.ReadWriter) error {
	if !state.DBG_trace_tc {
		if err := writeResponse(rw, errResponse("tracing not enabled; restart with --dbg-trace-tc")); err != nil {
			return err
		}
		return device.ErrIPCStatusHandled
	}
	// Send initial OK
	if err := writeResponse(rw, &protocol.IpcResponse{Ok: true}); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_, _ = rw.ReadByte() // wait for EOF / disconnect
		cancel()
	}()
	ch := make(chan interface{})
	n.Trace.Register(ch)
	defer n.Trace.Unregister(ch)
	for {
		select {
		case <-ctx.Done():
			return device.ErrIPCStatusHandled
		case msg := <-ch:
			if str, ok := msg.(string); ok {
				resp := &protocol.IpcResponse{
					Ok:       true,
					Response: &protocol.IpcResponse_Trace{Trace: &protocol.TraceEvent{Line: str}},
				}
				if err := writeResponse(rw, resp); err != nil {
					return device.ErrIPCStatusHandled
				}
			}
		}
	}
}
