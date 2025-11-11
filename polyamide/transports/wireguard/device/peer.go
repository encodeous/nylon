/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2025 WireGuard LLC. All Rights Reserved.
 */

package device

import (
	"container/list"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/encodeous/nylon/perf"
	"github.com/encodeous/nylon/polyamide/conn"
)

type Peer struct {
	isRunning         atomic.Bool
	keypairs          Keypairs
	handshake         Handshake
	device            *Device
	stopping          sync.WaitGroup // routines pending stop
	txBytes           atomic.Uint64  // bytes send to peer (endpoint)
	rxBytes           atomic.Uint64  // bytes received from peer
	lastHandshakeNano atomic.Int64   // nano seconds since epoch

	endpoints struct {
		sync.Mutex
		val            []conn.Endpoint
		clearSrcOnTx   bool // signal to val.ClearSrc() prior to next packet transmission
		disableRoaming bool
		lastInitIndex  int
		preferRoaming  bool // the endpoint from roaming will be set as the first index
	}

	timers struct {
		retransmitHandshake     *Timer
		sendKeepalive           *Timer
		newHandshake            *Timer
		zeroKeyMaterial         *Timer
		persistentKeepalive     *Timer
		lastReceived            atomic.Int64
		handshakeAttempts       atomic.Uint32
		needAnotherKeepalive    atomic.Bool
		sentLastMinuteHandshake atomic.Bool
	}

	state struct {
		sync.Mutex // protects against concurrent Start/Stop
	}

	queue struct {
		staged   chan *QueueOutboundElementsContainer // staged packets before a handshake is available
		outbound *autodrainingOutboundQueue           // sequential ordering of udp transmission
		inbound  *autodrainingInboundQueue            // sequential ordering of tun writing
	}

	cookieGenerator             CookieGenerator
	trieEntries                 list.List
	persistentKeepaliveInterval atomic.Uint32
}

func (device *Device) NewPeer(pk NoisePublicKey) (*Peer, error) {
	if device.isClosed() {
		return nil, errors.New("device closed")
	}

	// lock resources
	device.staticIdentity.RLock()
	defer device.staticIdentity.RUnlock()

	device.peers.Lock()
	defer device.peers.Unlock()

	// check if over limit
	if len(device.peers.keyMap) >= MaxPeers {
		return nil, errors.New("too many peers")
	}

	// create peer
	peer := new(Peer)

	peer.cookieGenerator.Init(pk)
	peer.device = device
	peer.queue.outbound = newAutodrainingOutboundQueue(device)
	peer.queue.inbound = newAutodrainingInboundQueue(device)
	peer.queue.staged = make(chan *QueueOutboundElementsContainer, QueueStagedSize)

	// map public key
	_, ok := device.peers.keyMap[pk]
	if ok {
		return nil, errors.New("adding existing peer")
	}

	// pre-compute DH
	handshake := &peer.handshake
	handshake.mutex.Lock()
	handshake.precomputedStaticStatic, _ = device.staticIdentity.privateKey.sharedSecret(pk)
	handshake.remoteStatic = pk
	handshake.mutex.Unlock()

	// reset endpoints
	peer.endpoints.Lock()
	peer.endpoints.val = peer.endpoints.val[:0]
	peer.endpoints.disableRoaming = false
	peer.endpoints.clearSrcOnTx = false
	peer.endpoints.Unlock()

	// init timers
	peer.timersInit()

	// add
	device.peers.keyMap[pk] = peer

	return peer, nil
}

func (peer *Peer) SendBuffers(buffers [][]byte, eps []conn.Endpoint) error {
	peer.device.net.RLock()
	defer peer.device.net.RUnlock()

	if peer.device.isClosed() {
		return nil
	}

	peer.endpoints.Lock()
	endpoints := peer.endpoints.val
	if len(endpoints) == 0 {
		peer.endpoints.Unlock()
		return errors.New("no known endpoints for peer")
	}
	if peer.endpoints.clearSrcOnTx {
		for _, ep := range endpoints {
			ep.ClearSrc()
		}
		for _, ep := range eps {
			if ep != nil {
				ep.ClearSrc()
			}
		}
		peer.endpoints.clearSrcOnTx = false
	}
	peer.endpoints.Unlock()

	// optimization, if multiple contiguous buffers share the same endpoint, send them in a single batch
	prevIdx := 0
	prevEp := eps[0]

	var anyError error
	for i := 0; i <= len(buffers); i++ {
		if i == len(buffers) || eps[i] != prevEp {
			// send batch from prevIdx to i-1
			if prevEp == nil {
				prevEp = endpoints[0] // default endpoint
			}
			err := peer.device.net.bind.Send(buffers[prevIdx:i], prevEp)
			if err != nil {
				anyError = err
			}
			prevIdx = i
			if i < len(buffers) {
				prevEp = eps[i]
			}
		}
	}

	var totalLen uint64
	for _, b := range buffers {
		totalLen += uint64(len(b))
	}
	perf.SentPacketPerSecond.Add(float64(len(buffers)))
	perf.SentBytesPerSecond.Add(float64(totalLen))
	peer.txBytes.Add(totalLen)
	return anyError
}

func (peer *Peer) GetPublicKey() NoisePublicKey {
	return peer.handshake.remoteStatic
}

func (peer *Peer) String() string {
	// The awful goo that follows is identical to:
	//
	//   base64Key := base64.StdEncoding.EncodeToString(peer.handshake.remoteStatic[:])
	//   abbreviatedKey := base64Key[0:4] + "…" + base64Key[39:43]
	//   return fmt.Sprintf("peer(%s)", abbreviatedKey)
	//
	// except that it is considerably more efficient.
	src := peer.handshake.remoteStatic
	b64 := func(input byte) byte {
		return input + 'A' + byte(((25-int(input))>>8)&6) - byte(((51-int(input))>>8)&75) - byte(((61-int(input))>>8)&15) + byte(((62-int(input))>>8)&3)
	}
	b := []byte("peer(____…____)")
	const first = len("peer(")
	const second = len("peer(____…")
	b[first+0] = b64((src[0] >> 2) & 63)
	b[first+1] = b64(((src[0] << 4) | (src[1] >> 4)) & 63)
	b[first+2] = b64(((src[1] << 2) | (src[2] >> 6)) & 63)
	b[first+3] = b64(src[2] & 63)
	b[second+0] = b64(src[29] & 63)
	b[second+1] = b64((src[30] >> 2) & 63)
	b[second+2] = b64(((src[30] << 4) | (src[31] >> 4)) & 63)
	b[second+3] = b64((src[31] << 2) & 63)
	return string(b)
}

func (peer *Peer) Start() {
	// should never start a peer on a closed device
	if peer.device.isClosed() {
		return
	}

	// prevent simultaneous start/stop operations
	peer.state.Lock()
	defer peer.state.Unlock()

	if peer.isRunning.Load() {
		return
	}

	device := peer.device
	device.Log.Verbosef("%v - Starting", peer)

	// reset routine state
	peer.stopping.Wait()
	peer.stopping.Add(2)

	peer.handshake.mutex.Lock()
	peer.handshake.lastSentHandshake = time.Now().Add(-(RekeyTimeout + time.Second))
	peer.handshake.mutex.Unlock()

	peer.device.queue.encryption.wg.Add(1) // keep encryption queue open for our writes

	peer.timersStart()

	device.flushInboundQueue(peer.queue.inbound)
	device.flushOutboundQueue(peer.queue.outbound)

	// Use the device batch size, not the bind batch size, as the device size is
	// the size of the batch pools.
	batchSize := peer.device.BatchSize()
	go peer.RoutineSequentialSender(batchSize)
	go peer.RoutineSequentialReceiver(batchSize)

	peer.isRunning.Store(true)
}

func (peer *Peer) ZeroAndFlushAll() {
	device := peer.device

	// clear key pairs

	keypairs := &peer.keypairs
	keypairs.Lock()
	device.DeleteKeypair(keypairs.previous)
	device.DeleteKeypair(keypairs.current)
	device.DeleteKeypair(keypairs.next.Load())
	keypairs.previous = nil
	keypairs.current = nil
	keypairs.next.Store(nil)
	keypairs.Unlock()

	// clear handshake state

	handshake := &peer.handshake
	handshake.mutex.Lock()
	device.indexTable.Delete(handshake.localIndex)
	handshake.Clear()
	handshake.mutex.Unlock()

	peer.FlushStagedPackets()
}

func (peer *Peer) ExpireCurrentKeypairs() {
	handshake := &peer.handshake
	handshake.mutex.Lock()
	peer.device.indexTable.Delete(handshake.localIndex)
	handshake.Clear()
	peer.handshake.lastSentHandshake = time.Now().Add(-(RekeyTimeout + time.Second))
	handshake.mutex.Unlock()

	keypairs := &peer.keypairs
	keypairs.Lock()
	if keypairs.current != nil {
		keypairs.current.sendNonce.Store(RejectAfterMessages)
	}
	if next := keypairs.next.Load(); next != nil {
		next.sendNonce.Store(RejectAfterMessages)
	}
	keypairs.Unlock()
}

func (peer *Peer) Stop() {
	peer.state.Lock()
	defer peer.state.Unlock()

	if !peer.isRunning.Swap(false) {
		return
	}

	peer.device.Log.Verbosef("%v - Stopping", peer)

	peer.timersStop()
	// Signal that RoutineSequentialSender and RoutineSequentialReceiver should exit.
	peer.queue.inbound.c <- nil
	peer.queue.outbound.c <- nil
	peer.stopping.Wait()
	peer.device.queue.encryption.wg.Done() // no more writes to encryption queue from us

	peer.ZeroAndFlushAll()
}

func (peer *Peer) SetEndpointFromPacket(endpoint conn.Endpoint) {
	peer.endpoints.Lock()
	defer peer.endpoints.Unlock()
	if peer.endpoints.disableRoaming {
		return
	}
	peer.endpoints.clearSrcOnTx = false

	if peer.endpoints.preferRoaming || len(peer.endpoints.val) == 0 {
		if len(peer.endpoints.val) == 0 {
			peer.endpoints.val = append(peer.endpoints.val, endpoint)
		} else {
			peer.endpoints.val[0] = endpoint
		}
	}
}

// SetEndpoints configures the endpoints of the peer. The first endpoint will be the default endpoint used for packet routing
func (peer *Peer) SetEndpoints(endpoints []conn.Endpoint) {
	peer.endpoints.Lock()
	defer peer.endpoints.Unlock()
	peer.endpoints.val = endpoints
}

func (peer *Peer) GetEndpoints() []conn.Endpoint {
	peer.handshake.mutex.RLock()
	defer peer.handshake.mutex.RUnlock()
	return slices.Clone(peer.endpoints.val)
}

func (peer *Peer) CleanEndpoints() {
	peer.handshake.mutex.Lock()
	defer peer.handshake.mutex.Unlock()
	if len(peer.endpoints.val) > 1 {
		peer.endpoints.val = peer.endpoints.val[:1]
	}
}

func (peer *Peer) SetPreferRoaming(val bool) {
	peer.endpoints.Lock()
	defer peer.endpoints.Unlock()
	peer.endpoints.preferRoaming = val
}

func (peer *Peer) GetPreferRoaming() bool {
	peer.handshake.mutex.RLock()
	defer peer.handshake.mutex.RUnlock()
	return peer.endpoints.preferRoaming
}

func (peer *Peer) markEndpointSrcForClearing() {
	peer.endpoints.Lock()
	defer peer.endpoints.Unlock()
	if len(peer.endpoints.val) == 0 {
		return
	}
	peer.endpoints.clearSrcOnTx = true
}

func (peer *Peer) LastReceivedPacket() time.Time {
	nano := peer.timers.lastReceived.Load()
	return time.Unix(0, nano)
}

func (peer *Peer) SetPersistentKeepaliveInterval(interval time.Duration) {
	old := peer.persistentKeepaliveInterval.Swap(uint32(interval.Seconds()))

	// Send immediate keepalive if we're turning it on and before it wasn't on.
	if old == 0 && interval.Seconds() != 0 {
		peer.SendKeepalive()
		peer.SendStagedPackets()
	}
}
