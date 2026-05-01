package core

import (
	"net/netip"
	"time"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/gaissmai/bart"
	"google.golang.org/protobuf/proto"

	"github.com/encodeous/nylon/log"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
)

type RouteTableEntry struct {
	Nh        state.NodeId
	Peer      *device.Peer
	Blackhole bool
}

func (n *Nylon) GetNeighIO(neigh state.NodeId) *IOPending {
	nio, ok := n.router.IO[neigh]
	if !ok {
		nio = &IOPending{
			SeqnoReq:   make(map[state.Source]state.Pair[uint16, uint8]),
			SeqnoDedup: ttlcache.New[state.Source, uint16](ttlcache.WithTTL[state.Source, uint16](state.SeqnoDedupTTL), ttlcache.WithDisableTouchOnHit[state.Source, uint16]()),
			Acks:       make(map[netip.Prefix]struct{}),
			Updates:    make(map[netip.Prefix]*protocol.Ny_Update),
		}
		n.router.IO[neigh] = nio
	}
	n.router.IO[neigh] = nio
	return nio
}

func (n *Nylon) SendRouteUpdate(neigh state.NodeId, advRoute state.PubRoute) {
	nio := n.GetNeighIO(neigh)
	prefix, _ := advRoute.Prefix.MarshalBinary()
	nio.Updates[advRoute.Prefix] = &protocol.Ny_Update{
		RouterId: string(advRoute.NodeId),
		Prefix:   prefix,
		Seqno:    uint32(advRoute.Seqno),
		Metric:   advRoute.Metric,
	}
}

func (n *Nylon) SendAckRetract(neigh state.NodeId, prefix netip.Prefix) {
	nio := n.GetNeighIO(neigh)
	nio.Acks[prefix] = struct{}{}
}

func (n *Nylon) BroadcastSendRouteUpdate(advRoute state.PubRoute) {
	for _, neigh := range n.RouterState.Neighbours {
		n.SendRouteUpdate(neigh.Id, advRoute)
	}
}

func (n *Nylon) RequestSeqno(neigh state.NodeId, src state.Source, seqno uint16, hopCnt uint8) {
	nio := n.GetNeighIO(neigh)
	old := nio.SeqnoDedup.Get(src)
	maxSeq := seqno
	if old != nil {
		maxSeq = max(seqno, old.Value())
		if SeqnoGe(old.Value(), seqno) {
			return // we have already sent such a request before
		}
	}
	nio.SeqnoDedup.Set(src, maxSeq, ttlcache.DefaultTTL)
	req, ok := nio.SeqnoReq[src]
	if !ok || seqno < req.V1 {
		req = state.Pair[uint16, uint8]{V1: seqno, V2: hopCnt}
	} else {
		if hopCnt > req.V2 {
			req.V2 = hopCnt
		}
	}
	nio.SeqnoReq[src] = req
}

func (n *Nylon) BroadcastRequestSeqno(src state.Source, seqno uint16, hopCnt uint8) {
	for _, neigh := range n.RouterState.Neighbours {
		n.RequestSeqno(neigh.Id, src, seqno, hopCnt)
	}
}

func (n *Nylon) RouterEvent(event string, desc string, args ...any) {
	if event == log.EventNoEndpointToNeigh {
		return // ignored
	}
	n.router.log.Debug(desc, append([]any{"event", event}, args...)...)
}

func (n *Nylon) UpdateNeighbour(neigh state.NodeId) {
	PushFullTable(n.RouterState, n, neigh)
}

func (n *Nylon) TableInsertRoute(prefix netip.Prefix, route state.SelRoute) {
	nh := route.Nh
	if route.Metric == state.INF {
		n.router.ForwardTable.Insert(prefix, RouteTableEntry{
			Nh:        nh,
			Blackhole: true,
		})
		n.router.ExitTable.Delete(prefix)
		return
	}
	peer := n.Device.LookupPeer(device.NoisePublicKey(n.GetNode(nh).PubKey))
	n.router.ForwardTable.Insert(prefix, RouteTableEntry{
		Nh:   nh,
		Peer: peer,
	})
	if route.Nh == n.LocalCfg.Id {
		n.router.ExitTable.Insert(prefix, RouteTableEntry{
			Nh:   nh,
			Peer: peer,
		})
	} else {
		n.router.ExitTable.Delete(prefix)
	}
}

func (n *Nylon) TableDeleteRoute(prefix netip.Prefix) {
	n.router.ForwardTable.Delete(prefix)
	n.router.ExitTable.Delete(prefix)
}

type IOPending struct {
	// SeqnoReq values represent a pair of (seqno, hop count)
	SeqnoReq   map[state.Source]state.Pair[uint16, uint8]
	SeqnoDedup *ttlcache.Cache[state.Source, uint16]
	Acks       map[netip.Prefix]struct{}
	Updates    map[netip.Prefix]*protocol.Ny_Update
}

func (n *Nylon) CleanupRouter() error {
	n.router.log = nil
	n.router.IO = nil
	return nil
}

func (n *Nylon) GcRouter() error {
	RunGC(n.RouterState, n)
	for id, _ := range n.router.IO {
		if n.RouterState.GetNeighbour(id) == nil {
			delete(n.router.IO, id)
			continue
		}
	}
	for _, nio := range n.router.IO {
		nio.SeqnoDedup.DeleteExpired()
	}
	return nil
}

func (n *Nylon) InitRouter() error {
	n.router.log = n.Log.With("module", log.ScopeRouter)
	n.router.log.Debug("init router")
	n.router.IO = make(map[state.NodeId]*IOPending)
	n.router.ForwardTable = bart.Table[RouteTableEntry]{}
	n.router.ExitTable = bart.Table[RouteTableEntry]{}
	n.RouterState = &state.RouterState{
		Id:         n.LocalCfg.Id,
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: make([]*state.Neighbour, 0),
		Advertised: make(map[netip.Prefix]state.Advertisement),
	}
	maxTime := time.Unix(1<<63-62135596801, 999999999)
	for _, prefix := range n.GetRouter(n.LocalCfg.Id).Prefixes {
		n.RouterState.Advertised[prefix.GetPrefix()] = state.Advertisement{
			NodeId:        n.LocalCfg.Id,
			Expiry:        maxTime,
			IsPassiveHold: false,
			MetricFn:      prefix.GetMetric,
		}
	}

	n.router.log.Debug("schedule router tasks")

	n.RepeatTask(func() error {
		FullTableUpdate(n.RouterState, n)
		return nil
	}, state.RouteUpdateDelay)
	n.RepeatTask(func() error {
		SolveStarvation(n.RouterState, n)
		return nil
	}, state.StarvationDelay)

	n.RepeatTask(func() error {
		return n.flushIO()
	}, state.NeighbourIOFlushDelay)
	return nil
}

// ComputeSysRouteTable computes: computed = prefixes - (((n.CentralCfg.ExcludeIPs U selected self prefixes) - n.LocalCfg.UnexcludeIPs) U n.LocalCfg.ExcludeIPs)
func (n *Nylon) ComputeSysRouteTable() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0)
	selectedSelf := make(map[netip.Prefix]struct{})
	for entry, v := range n.RouterState.Routes {
		prefixes = append(prefixes, entry)
		if v.Nh == n.LocalCfg.Id {
			selectedSelf[entry] = struct{}{}
		}
	}

	defaultExcludes := n.CentralCfg.ExcludeIPs
	for p := range selectedSelf {
		defaultExcludes = append(defaultExcludes, p)
	}
	exclude := append(state.SubtractPrefix(defaultExcludes, n.LocalCfg.UnexcludeIPs), n.LocalCfg.ExcludeIPs...)
	return state.SubtractPrefix(prefixes, exclude)
}

func (n *Nylon) updatePassiveClient(prefix state.PrefixHealthWrapper, node state.NodeId, passiveHold bool) {
	// inserts an artificial route into the table

	hasPassiveHold := false
	old, ok := n.RouterState.Advertised[prefix.GetPrefix()]
	if ok && old.NodeId == node {
		hasPassiveHold = old.IsPassiveHold
	}

	if passiveHold && !hasPassiveHold {
		// the first time we enter passive hold, we should increment the seqno to prevent other nodes from switching away from the route
		// this reduces a lot of route flapping when the client wakes up, sends some traffic and then goes back to sleep
		n.RouterState.SetSeqno(prefix.GetPrefix(), n.RouterState.GetSeqno(prefix.GetPrefix())+1)
	}

	// passive nodes may only have static prefixes, so we don't call prefix.Start()
	n.RouterState.Advertised[prefix.GetPrefix()] = state.Advertisement{
		NodeId:        node,
		Expiry:        time.Now().Add(state.ClientKeepaliveInterval),
		IsPassiveHold: passiveHold,
		MetricFn:      prefix.GetMetric,
		ExpiryFn: func() {
			// we didn't start the prefix monitoring
		},
	}
}

func (n *Nylon) hasRecentlyAdvertised(prefix netip.Prefix) bool {
	adv, ok := n.RouterState.Advertised[prefix]
	if !ok {
		return false
	}
	return time.Now().Before(adv.Expiry)
}

func (n *Nylon) checkNeigh(id state.NodeId) bool {
	for _, node := range n.RouterState.Neighbours {
		if node.Id == id {
			return true
		}
	}
	n.router.log.Warn("received packet from unknown neighbour", "from", id)
	return false
}

func (n *Nylon) checkPrefix(prefix netip.Prefix) bool {
	for _, p := range n.GetPrefixes() {
		if p == prefix {
			return true
		}
	}
	n.router.log.Warn("received packet for unknown prefix", "prefix", prefix)
	return false
}

func (n *Nylon) checkNode(id state.NodeId) bool {
	ncfg := n.TryGetNode(id)
	if ncfg == nil {
		n.router.log.Warn("received packet from unknown node", "from", id)
	}
	return ncfg != nil
}

// packet handlers
func (n *Nylon) routerHandleRouteUpdate(node state.NodeId, update *protocol.Ny_Update) error {
	prefix := netip.Prefix{}
	err := prefix.UnmarshalBinary(update.Prefix)
	if err != nil {
		n.router.log.Warn("received update with invalid prefix", "prefix", update.Prefix, "err", err)
		return nil
	}
	if !n.checkNeigh(node) ||
		!n.checkPrefix(prefix) ||
		!n.checkNode(state.NodeId(update.RouterId)) {
		return nil
	}
	HandleNeighbourUpdate(n.RouterState, n, node, state.PubRoute{
		Source: state.Source{
			NodeId: state.NodeId(update.RouterId),
			Prefix: prefix,
		},
		FD: state.FD{
			Seqno:  uint16(update.Seqno),
			Metric: update.Metric,
		},
	})
	ComputeRoutes(n.RouterState, n)
	return nil
}

func (n *Nylon) routerHandleAckRetract(neigh state.NodeId, update *protocol.Ny_AckRetract) error {
	prefix := netip.Prefix{}
	err := prefix.UnmarshalBinary(update.Prefix)
	if err != nil {
		n.router.log.Warn("received ack retract with invalid prefix", "prefix", update.Prefix, "err", err)
		return nil
	}
	if !n.checkPrefix(prefix) ||
		!n.checkNeigh(neigh) {
		return nil
	}
	HandleAckRetract(n.RouterState, n, neigh, prefix)
	return nil
}

func (n *Nylon) routerHandleSeqnoRequest(neigh state.NodeId, pkt *protocol.Ny_SeqnoRequest) error {
	prefix := netip.Prefix{}
	err := prefix.UnmarshalBinary(pkt.Prefix)
	if err != nil {
		n.router.log.Warn("received seqno request with invalid prefix", "prefix", pkt.Prefix, "err", err)
		return nil
	}
	if !n.checkNeigh(neigh) ||
		!n.checkPrefix(prefix) ||
		!n.checkNode(state.NodeId(pkt.RouterId)) {
		return nil
	}
	HandleSeqnoRequest(n.RouterState, n, neigh, state.Source{
		NodeId: state.NodeId(pkt.RouterId),
		Prefix: prefix,
	}, uint16(pkt.Seqno), uint8(pkt.HopCount))
	return nil
}

func (n *Nylon) flushIO() error {
	for _, neigh := range n.RouterState.Neighbours {
		// TODO, investigate effect of packet loss on control messages
		best := neigh.BestEndpoint()
		nio := n.GetNeighIO(neigh.Id)
		if nio == nil {
			continue
		}
		if best != nil && best.IsActive() {
			peer := n.Device.LookupPeer(device.NoisePublicKey(n.GetNode(neigh.Id).PubKey))
			for {
				bundle := &protocol.TransportBundle{}
				tLength := 0

				// we can coalesce messages, but we need to make sure we don't fragment our UDP packet

				for seqR, _ := range nio.SeqnoReq {
					prefixBytes, _ := seqR.Prefix.MarshalBinary()
					req := &protocol.Ny{Type: &protocol.Ny_SeqnoRequestOp{
						SeqnoRequestOp: &protocol.Ny_SeqnoRequest{
							RouterId: string(seqR.NodeId),
							Prefix:   prefixBytes,
							Seqno:    uint32(nio.SeqnoReq[seqR].V1),
							HopCount: uint32(nio.SeqnoReq[seqR].V2),
						},
					}}
					if tLength+proto.Size(req) >= state.SafeMTU {
						goto send
					}
					delete(nio.SeqnoReq, seqR)
					bundle.Packets = append(bundle.Packets, req)
					tLength += proto.Size(req)
				}

				for id, update := range nio.Updates {
					req := &protocol.Ny{Type: &protocol.Ny_RouteOp{
						RouteOp: update,
					}}
					if tLength+proto.Size(req) >= state.SafeMTU {
						goto send
					}
					delete(nio.Updates, id)
					bundle.Packets = append(bundle.Packets, req)
					tLength += proto.Size(req)
				}

				if tLength == 0 {
					break
				}
			send:
				err := n.SendNylonBundle(bundle, nil, peer)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}
