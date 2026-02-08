package core

import (
	"fmt"
	"net/netip"

	"github.com/encodeous/nylon/polyamide/device"
	"github.com/gaissmai/bart"
	"google.golang.org/protobuf/proto"

	//"errors"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/jellydator/ttlcache/v3"
	//"slices"
	"time"
)

type NylonRouter struct {
	*state.State
	LastStarvationRequest time.Time
	IO                    map[state.NodeId]*IOPending
	// ForwardTable contains the full routing table
	ForwardTable bart.Table[RouteTableEntry]
	// ExitTable contains only routes to services hosted on this node
	ExitTable bart.Table[RouteTableEntry]
}

type RouteTableEntry struct {
	Nh   state.NodeId
	Peer *device.Peer
}

func (r *NylonRouter) GetNeighIO(neigh state.NodeId) *IOPending {
	nio, ok := r.IO[neigh]
	if !ok {
		nio = &IOPending{
			SeqnoReq:   make(map[state.Source]state.Pair[uint16, uint8]),
			SeqnoDedup: ttlcache.New[state.Source, uint16](ttlcache.WithTTL[state.Source, uint16](state.SeqnoDedupTTL), ttlcache.WithDisableTouchOnHit[state.Source, uint16]()),
			Acks:       make(map[netip.Prefix]struct{}),
			Updates:    make(map[netip.Prefix]*protocol.Ny_Update),
		}
		r.IO[neigh] = nio
	}
	r.IO[neigh] = nio
	return nio
}

func (r *NylonRouter) SendRouteUpdate(neigh state.NodeId, advRoute state.PubRoute) {
	nio := r.GetNeighIO(neigh)
	prefix, _ := advRoute.Prefix.MarshalBinary()
	nio.Updates[advRoute.Prefix] = &protocol.Ny_Update{
		RouterId: string(advRoute.NodeId),
		Prefix:   prefix,
		Seqno:    uint32(advRoute.Seqno),
		Metric:   advRoute.Metric,
	}
}

func (r *NylonRouter) SendAckRetract(neigh state.NodeId, prefix netip.Prefix) {
	nio := r.GetNeighIO(neigh)
	nio.Acks[prefix] = struct{}{}
}

func (r *NylonRouter) BroadcastSendRouteUpdate(advRoute state.PubRoute) {
	for neigh := range r.IO {
		r.SendRouteUpdate(neigh, advRoute)
	}
}

func (r *NylonRouter) RequestSeqno(neigh state.NodeId, src state.Source, seqno uint16, hopCnt uint8) {
	nio := r.GetNeighIO(neigh)
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

func (r *NylonRouter) BroadcastRequestSeqno(src state.Source, seqno uint16, hopCnt uint8) {
	for neigh := range r.IO {
		r.RequestSeqno(neigh, src, seqno, hopCnt)
	}
}

func (r *NylonRouter) Log(event RouterEvent, desc string, args ...any) {
	r.Env.Log.Debug(fmt.Sprintf("%s %s", event.String(), desc), args...)
}

func (r *NylonRouter) UpdateNeighbour(neigh state.NodeId) {
	PushFullTable(r.RouterState, r, neigh)
}

func (r *NylonRouter) TableInsertRoute(prefix netip.Prefix, route state.SelRoute) {
	n := Get[*Nylon](r.State)
	nh := route.Nh
	peer := n.Device.LookupPeer(device.NoisePublicKey(r.GetNode(nh).PubKey))
	r.ForwardTable.Insert(prefix, RouteTableEntry{
		Nh:   nh,
		Peer: peer,
	})
	if route.Nh == r.Id {
		r.ExitTable.Insert(prefix, RouteTableEntry{
			Nh:   nh,
			Peer: peer,
		})
	} else {
		r.ExitTable.Delete(prefix)
	}
}

func (r *NylonRouter) TableDeleteRoute(prefix netip.Prefix) {
	r.ForwardTable.Delete(prefix)
	r.ExitTable.Delete(prefix)
}

type IOPending struct {
	// SeqnoReq values represent a pair of (seqno, hop count)
	SeqnoReq   map[state.Source]state.Pair[uint16, uint8]
	SeqnoDedup *ttlcache.Cache[state.Source, uint16]
	Acks       map[netip.Prefix]struct{}
	Updates    map[netip.Prefix]*protocol.Ny_Update
}

func (r *NylonRouter) Cleanup(s *state.State) error {
	r.State = nil
	r.IO = nil
	return nil
}

func (r *NylonRouter) GcRouter(s *state.State) error {
	RunGC(s.RouterState, r)
	for id, _ := range r.IO {
		if s.GetNeighbour(id) == nil {
			delete(r.IO, id)
			continue
		}
	}
	for _, nio := range r.IO {
		nio.SeqnoDedup.DeleteExpired()
	}
	return nil
}

func (r *NylonRouter) Init(s *state.State) error {
	s.Log.Debug("init router")
	r.State = s
	r.IO = make(map[state.NodeId]*IOPending)
	r.ForwardTable = bart.Table[RouteTableEntry]{}
	s.RouterState = &state.RouterState{
		Id:         s.Env.LocalCfg.Id,
		SelfSeqno:  make(map[netip.Prefix]uint16),
		Routes:     make(map[netip.Prefix]state.SelRoute),
		Sources:    make(map[state.Source]state.FD),
		Neighbours: make([]*state.Neighbour, 0),
		Advertised: make(map[netip.Prefix]state.Advertisement),
	}
	maxTime := time.Unix(1<<63-62135596801, 999999999)
	for _, prefix := range s.Env.GetRouter(s.Id).Prefixes {
		s.RouterState.Advertised[prefix.GetPrefix()] = state.Advertisement{
			NodeId:        s.Id,
			Expiry:        maxTime,
			IsPassiveHold: false,
			MetricFn:      prefix.GetMetric,
		}
	}

	s.Log.Debug("schedule router tasks")

	s.Env.RepeatTask(func(s *state.State) error {
		FullTableUpdate(s.RouterState, r)
		return nil
	}, state.RouteUpdateDelay)
	s.Env.RepeatTask(func(s *state.State) error {
		SolveStarvation(s.RouterState, r)
		return nil
	}, state.StarvationDelay)

	s.Env.RepeatTask(flushIO, state.NeighbourIOFlushDelay)
	return nil
}

// ComputeSysRouteTable computes: computed = prefixes - (((r.CentralCfg.ExcludeIPs U selected self prefixes) - r.LocalCfg.UnexcludeIPs) U r.LocalCfg.ExcludeIPs)
func (r *NylonRouter) ComputeSysRouteTable() []netip.Prefix {
	prefixes := make([]netip.Prefix, 0)
	selectedSelf := make(map[netip.Prefix]struct{})
	for entry, v := range r.Routes {
		prefixes = append(prefixes, entry)
		if v.Nh == r.Id {
			selectedSelf[entry] = struct{}{}
		}
	}

	defaultExcludes := r.CentralCfg.ExcludeIPs
	for p := range selectedSelf {
		defaultExcludes = append(defaultExcludes, p)
	}
	exclude := append(state.SubtractPrefix(defaultExcludes, r.LocalCfg.UnexcludeIPs), r.LocalCfg.ExcludeIPs...)
	return state.SubtractPrefix(prefixes, exclude)
}

func (r *NylonRouter) updatePassiveClient(s *state.State, prefix state.PrefixHealthWrapper, node state.NodeId, passiveHold bool) {
	// inserts an artificial route into the table

	hasPassiveHold := false
	old, ok := s.RouterState.Advertised[prefix.GetPrefix()]
	if ok && old.NodeId == node {
		hasPassiveHold = old.IsPassiveHold
	}

	if passiveHold && !hasPassiveHold {
		// the first time we enter passive hold, we should increment the seqno to prevent other nodes from switching away from the route
		// this reduces a lot of route flapping when the client wakes up, sends some traffic and then goes back to sleep
		r.SetSeqno(prefix.GetPrefix(), s.RouterState.GetSeqno(prefix.GetPrefix())+1)
	}

	prefix.Start(s.Log)
	s.Advertised[prefix.GetPrefix()] = state.Advertisement{
		NodeId:        node,
		Expiry:        time.Now().Add(state.ClientKeepaliveInterval),
		IsPassiveHold: passiveHold,
		MetricFn:      prefix.GetMetric,
		ExpiryFn: func() {
			prefix.Stop()
		},
	}
}

func (r *NylonRouter) hasRecentlyAdvertised(prefix netip.Prefix) bool {
	adv, ok := r.RouterState.Advertised[prefix]
	if !ok {
		return false
	}
	return time.Now().Before(adv.Expiry)
}

func checkNeigh(s *state.State, id state.NodeId) bool {
	for _, n := range s.Neighbours {
		if n.Id == id {
			return true
		}
	}
	s.Log.Warn("received packet from unknown neighbour", "from", id)
	return false
}

func checkPrefix(s *state.State, prefix netip.Prefix) bool {
	for _, p := range s.GetPrefixes() {
		if p == prefix {
			return true
		}
	}
	s.Log.Warn("received packet for unknown prefix", "prefix", prefix)
	return false
}

func checkNode(s *state.State, id state.NodeId) bool {
	ncfg := s.TryGetNode(id)
	if ncfg == nil {
		s.Log.Warn("received packet from unknown node", "from", id)
	}
	return ncfg != nil
}

// packet handlers
func routerHandleRouteUpdate(s *state.State, node state.NodeId, update *protocol.Ny_Update) error {
	r := Get[*NylonRouter](s)
	prefix := netip.Prefix{}
	err := prefix.UnmarshalBinary(update.Prefix)
	if err != nil {
		s.Log.Warn("received update with invalid prefix", "prefix", update.Prefix, "err", err)
		return nil
	}
	if !checkNeigh(s, node) ||
		!checkPrefix(s, prefix) ||
		!checkNode(s, state.NodeId(update.RouterId)) {
		return nil
	}
	HandleNeighbourUpdate(s.RouterState, r, node, state.PubRoute{
		Source: state.Source{
			NodeId: state.NodeId(update.RouterId),
			Prefix: prefix,
		},
		FD: state.FD{
			Seqno:  uint16(update.Seqno),
			Metric: update.Metric,
		},
	})
	return nil
}

func routerHandleAckRetract(s *state.State, neigh state.NodeId, update *protocol.Ny_AckRetract) error {
	r := Get[*NylonRouter](s)
	prefix := netip.Prefix{}
	err := prefix.UnmarshalBinary(update.Prefix)
	if err != nil {
		s.Log.Warn("received ack retract with invalid prefix", "prefix", update.Prefix, "err", err)
		return nil
	}
	if !checkPrefix(s, prefix) ||
		!checkNeigh(s, neigh) {
		return nil
	}
	HandleAckRetract(s.RouterState, r, neigh, prefix)
	return nil
}

func routerHandleSeqnoRequest(s *state.State, neigh state.NodeId, pkt *protocol.Ny_SeqnoRequest) error {
	r := Get[*NylonRouter](s)
	prefix := netip.Prefix{}
	err := prefix.UnmarshalBinary(pkt.Prefix)
	if err != nil {
		s.Log.Warn("received seqno request with invalid prefix", "prefix", pkt.Prefix, "err", err)
		return nil
	}
	if !checkNeigh(s, neigh) ||
		!checkPrefix(s, prefix) ||
		!checkNode(s, state.NodeId(pkt.RouterId)) {
		return nil
	}
	HandleSeqnoRequest(s.RouterState, r, neigh, state.Source{
		NodeId: state.NodeId(pkt.RouterId),
		Prefix: prefix,
	}, uint16(pkt.Seqno), uint8(pkt.HopCount))
	return nil
}

func flushIO(s *state.State) error {
	n := Get[*Nylon](s)
	r := Get[*NylonRouter](s)
	for _, neigh := range s.Neighbours {
		// TODO, investigate effect of packet loss on control messages
		best := neigh.BestEndpoint()
		nio := r.GetNeighIO(neigh.Id)
		if nio == nil {
			continue
		}
		if best != nil && best.IsActive() {
			peer := n.Device.LookupPeer(device.NoisePublicKey(n.env.GetNode(neigh.Id).PubKey))
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
