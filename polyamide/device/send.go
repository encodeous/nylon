/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2023 WireGuard LLC. All Rights Reserved.
 */

package device

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/encodeous/nylon/polyamide/conn"
	"github.com/encodeous/nylon/polyamide/tun"
	"golang.org/x/crypto/chacha20poly1305"
)

/* Outbound flow
 *
 * 1. TUN queue
 * 1.5 Traffic Control (sequential)
 * 2. Routing (sequential)
 * 3. Nonce assignment (sequential)
 * 4. Encryption (parallel)
 * 5. Transmission (sequential)
 *
 * The functions in this file occur (roughly) in the order in
 * which the packets are processed.
 *
 * Locking, Producers and Consumers
 *
 * The order of packets (per peer) must be maintained,
 * but encryption of packets happen out-of-order:
 *
 * The sequential consumers will attempt to take the lock,
 * workers release lock when they have completed work (encryption) on the packet.
 *
 * If the element is inserted into the "encryption queue",
 * the content is preceded by enough "junk" to contain the transport header
 * (to allow the construction of transport messages in-place)
 */

type QueueOutboundElement struct {
	buffer   *[MaxMessageSize]byte // slice holding the packet data
	packet   []byte                // slice of "buffer" (always!)
	nonce    uint64                // nonce for encryption
	keypair  *Keypair              // keypair for encryption
	peer     *Peer                 // related peer
	endpoint conn.Endpoint         // if the element is bound for a specific endpoint
}

type QueueOutboundElementsContainer struct {
	sync.Mutex
	elems []*QueueOutboundElement
}

func (device *Device) NewOutboundElement() *QueueOutboundElement {
	elem := device.GetOutboundElement()
	elem.buffer = device.GetMessageBuffer()
	elem.nonce = 0
	// keypair and peer were cleared (if necessary) by clearPointers.
	return elem
}

// clearPointers clears elem fields that contain pointers.
// This makes the garbage collector's life easier and
// avoids accidentally keeping other objects around unnecessarily.
// It also reduces the possible collateral damage from use-after-free bugs.
func (elem *QueueOutboundElement) clearPointers() {
	elem.buffer = nil
	elem.packet = nil
	elem.keypair = nil
	elem.peer = nil
	elem.endpoint = nil
}

/* Queues a keepalive if no packets are queued for peer
 */
func (peer *Peer) SendKeepalive() {
	if len(peer.queue.staged) == 0 && peer.isRunning.Load() {
		elem := peer.device.NewOutboundElement()
		elemsContainer := peer.device.GetOutboundElementsContainer()
		elemsContainer.elems = append(elemsContainer.elems, elem)
		select {
		case peer.queue.staged <- elemsContainer:
			peer.device.Log.Verbosef("%v - Sending keepalive packet", peer)
		default:
			peer.device.PutMessageBuffer(elem.buffer)
			peer.device.PutOutboundElement(elem)
			peer.device.PutOutboundElementsContainer(elemsContainer)
		}
	}
	peer.SendStagedPackets()
}

func (peer *Peer) SendHandshakeInitiation(isRetry bool) error {
	if !isRetry {
		peer.timers.handshakeAttempts.Store(0)
	}

	peer.handshake.mutex.RLock()
	if time.Since(peer.handshake.lastSentHandshake) < RekeyTimeout {
		peer.handshake.mutex.RUnlock()
		return nil
	}
	peer.handshake.mutex.RUnlock()

	peer.handshake.mutex.Lock()
	if time.Since(peer.handshake.lastSentHandshake) < RekeyTimeout {
		peer.handshake.mutex.Unlock()
		return nil
	}
	peer.handshake.lastSentHandshake = time.Now()
	peer.handshake.mutex.Unlock()

	peer.device.Log.Verbosef("%v - Sending handshake initiation", peer)

	msg, err := peer.device.CreateMessageInitiation(peer)
	if err != nil {
		peer.device.Log.Errorf("%v - Failed to create initiation message: %v", peer, err)
		return err
	}

	var buf [MessageInitiationSize]byte
	writer := bytes.NewBuffer(buf[:0])
	binary.Write(writer, binary.LittleEndian, msg)
	packet := writer.Bytes()
	peer.cookieGenerator.AddMacs(packet)

	peer.timersAnyAuthenticatedPacketTraversal(false)
	peer.timersAnyAuthenticatedPacketSent()

	// try a different index every time
	peer.endpoints.Lock()
	if len(peer.endpoints.val) == 0 {
		peer.device.Log.Verbosef("%v - Cannot send handshake initiation, no endpoints available", peer)
		peer.endpoints.Unlock()
		return nil
	}
	peer.endpoints.lastInitIndex = (peer.endpoints.lastInitIndex + 1) % len(peer.endpoints.val)
	selEp := []conn.Endpoint{peer.endpoints.val[peer.endpoints.lastInitIndex]}
	peer.endpoints.Unlock()

	err = peer.SendBuffers([][]byte{packet}, selEp)
	if err != nil {
		peer.device.Log.Verbosef("%v - Failed to send handshake initiation: %v", peer, err)
	}
	peer.timersHandshakeInitiated()

	return err
}

func (peer *Peer) SendHandshakeResponse(srcEndpoint conn.Endpoint) error {
	peer.handshake.mutex.Lock()
	peer.handshake.lastSentHandshake = time.Now()
	peer.handshake.mutex.Unlock()

	peer.device.Log.Verbosef("%v - Sending handshake response", peer)

	response, err := peer.device.CreateMessageResponse(peer)
	if err != nil {
		peer.device.Log.Errorf("%v - Failed to create response message: %v", peer, err)
		return err
	}

	var buf [MessageResponseSize]byte
	writer := bytes.NewBuffer(buf[:0])
	binary.Write(writer, binary.LittleEndian, response)
	packet := writer.Bytes()
	peer.cookieGenerator.AddMacs(packet)

	err = peer.BeginSymmetricSession()
	if err != nil {
		peer.device.Log.Errorf("%v - Failed to derive keypair: %v", peer, err)
		return err
	}

	peer.timersSessionDerived()
	peer.timersAnyAuthenticatedPacketTraversal(false)
	peer.timersAnyAuthenticatedPacketSent()

	// TODO: allocation could be avoided
	err = peer.SendBuffers([][]byte{packet}, []conn.Endpoint{srcEndpoint})
	if err != nil {
		peer.device.Log.Errorf("%v - Failed to send handshake response: %v", peer, err)
	}
	return err
}

func (device *Device) SendHandshakeCookie(initiatingElem *QueueHandshakeElement) error {
	device.Log.Verbosef("Sending cookie response for denied handshake message for %v", initiatingElem.endpoint.DstToString())

	sender := binary.LittleEndian.Uint32(initiatingElem.packet[4:8])
	reply, err := device.cookieChecker.CreateReply(initiatingElem.packet, sender, initiatingElem.endpoint.DstToBytes())
	if err != nil {
		device.Log.Errorf("Failed to create cookie reply: %v", err)
		return err
	}

	var buf [MessageCookieReplySize]byte
	writer := bytes.NewBuffer(buf[:0])
	binary.Write(writer, binary.LittleEndian, reply)
	// TODO: allocation could be avoided
	device.net.bind.Send([][]byte{writer.Bytes()}, initiatingElem.endpoint)
	return nil
}

func (peer *Peer) keepKeyFreshSending() {
	keypair := peer.keypairs.Current()
	if keypair == nil {
		return
	}
	nonce := keypair.sendNonce.Load()
	if nonce > RekeyAfterMessages || (keypair.isInitiator && time.Since(keypair.created) > RekeyAfterTime) {
		peer.SendHandshakeInitiation(false)
	}
}

func (device *Device) RoutineReadFromTUN() {
	defer func() {
		device.Log.Verbosef("Routine: TUN reader - stopped")
		device.state.stopping.Done()
		device.queue.encryption.wg.Done()
	}()

	device.Log.Verbosef("Routine: TUN reader - started")

	var (
		batchSize = device.BatchSize()
		readErr   error
		rBufs     = make([][]byte, batchSize)
		bufs      = make([]*[MaxMessageSize]byte, batchSize)
		count     = batchSize
		sizes     = make([]int, batchSize)
		tcBufs    = make([]*TCElement, 0, batchSize)
		offset    = MessageTransportHeaderSize
		tcs       = NewTCState()
	)

	for i := 0; i < batchSize; i++ {
		bufs[i] = device.GetMessageBuffer()
		rBufs[i] = bufs[i][:]
	}

	for {
		count, readErr = device.tun.device.Read(rBufs, sizes, offset)

		for i := 0; i < count; i++ {
			if sizes[i] < 1 {
				continue
			}
			tce := device.GetTCElement()
			tce.Buffer = bufs[i]
			tcBufs = append(tcBufs, tce)

			bufs[i] = device.GetMessageBuffer()
			rBufs[i] = bufs[i][:]
		}

		// pass to traffic control
		device.TCBatch(tcBufs, tcs)

		tcBufs = tcBufs[:0]

		if readErr != nil {
			if errors.Is(readErr, tun.ErrTooManySegments) {
				// TODO: record stat for this
				// This will happen if MSS is surprisingly small (< 576)
				// coincident with reasonably high throughput.
				device.Log.Verbosef("Dropped some packets from multi-segment read: %v", readErr)
				continue
			}
			if !device.isClosed() {
				if !errors.Is(readErr, os.ErrClosed) {
					device.Log.Errorf("Failed to read packet from TUN device: %v", readErr)
				}
				go device.Close()
			}
			return
		}
	}
}

func (peer *Peer) StagePackets(elems *QueueOutboundElementsContainer) {
	for {
		select {
		case peer.queue.staged <- elems:
			return
		default:
		}
		select {
		case tooOld := <-peer.queue.staged:
			for _, elem := range tooOld.elems {
				peer.device.PutMessageBuffer(elem.buffer)
				peer.device.PutOutboundElement(elem)
			}
			peer.device.PutOutboundElementsContainer(tooOld)
		default:
		}
	}
}

func (peer *Peer) SendStagedPackets() {
top:
	if len(peer.queue.staged) == 0 || !peer.device.isUp() {
		return
	}

	keypair := peer.keypairs.Current()
	if keypair == nil || keypair.sendNonce.Load() >= RejectAfterMessages || time.Since(keypair.created) >= RejectAfterTime {
		peer.SendHandshakeInitiation(false)
		return
	}

	for {
		var elemsContainerOOO *QueueOutboundElementsContainer
		select {
		case elemsContainer := <-peer.queue.staged:
			i := 0
			for _, elem := range elemsContainer.elems {
				elem.peer = peer
				elem.nonce = keypair.sendNonce.Add(1) - 1
				if elem.nonce >= RejectAfterMessages {
					keypair.sendNonce.Store(RejectAfterMessages)
					if elemsContainerOOO == nil {
						elemsContainerOOO = peer.device.GetOutboundElementsContainer()
					}
					elemsContainerOOO.elems = append(elemsContainerOOO.elems, elem)
					continue
				} else {
					elemsContainer.elems[i] = elem
					i++
				}

				elem.keypair = keypair
			}
			elemsContainer.Lock()
			elemsContainer.elems = elemsContainer.elems[:i]

			if elemsContainerOOO != nil {
				peer.StagePackets(elemsContainerOOO) // XXX: Out of order, but we can't front-load go chans
			}

			if len(elemsContainer.elems) == 0 {
				peer.device.PutOutboundElementsContainer(elemsContainer)
				goto top
			}

			// add to parallel and sequential queue
			if peer.isRunning.Load() {
				peer.queue.outbound.c <- elemsContainer
				peer.device.queue.encryption.c <- elemsContainer
			} else {
				for _, elem := range elemsContainer.elems {
					peer.device.PutMessageBuffer(elem.buffer)
					peer.device.PutOutboundElement(elem)
				}
				peer.device.PutOutboundElementsContainer(elemsContainer)
			}

			if elemsContainerOOO != nil {
				goto top
			}
		default:
			return
		}
	}
}

func (peer *Peer) FlushStagedPackets() {
	for {
		select {
		case elemsContainer := <-peer.queue.staged:
			for _, elem := range elemsContainer.elems {
				peer.device.PutMessageBuffer(elem.buffer)
				peer.device.PutOutboundElement(elem)
			}
			peer.device.PutOutboundElementsContainer(elemsContainer)
		default:
			return
		}
	}
}

func calculatePaddingSize(packetSize, mtu int) int {
	lastUnit := packetSize
	if mtu == 0 {
		return ((lastUnit + PaddingMultiple - 1) & ^(PaddingMultiple - 1)) - lastUnit
	}
	if lastUnit > mtu {
		lastUnit %= mtu
	}
	paddedSize := ((lastUnit + PaddingMultiple - 1) & ^(PaddingMultiple - 1))
	if paddedSize > mtu {
		paddedSize = mtu
	}
	return paddedSize - lastUnit
}

/* Encrypts the elements in the queue
 * and marks them for sequential consumption (by releasing the mutex)
 *
 * Obs. One instance per core
 */
func (device *Device) RoutineEncryption(id int) {
	var paddingZeros [PaddingMultiple]byte
	var nonce [chacha20poly1305.NonceSize]byte

	defer device.Log.Verbosef("Routine: encryption worker %d - stopped", id)
	device.Log.Verbosef("Routine: encryption worker %d - started", id)

	for elemsContainer := range device.queue.encryption.c {
		for _, elem := range elemsContainer.elems {
			// populate header fields
			header := elem.buffer[:MessageTransportHeaderSize]

			fieldType := header[0:4]
			fieldReceiver := header[4:8]
			fieldNonce := header[8:16]

			binary.LittleEndian.PutUint32(fieldType, MessageTransportType)
			binary.LittleEndian.PutUint32(fieldReceiver, elem.keypair.remoteIndex)
			binary.LittleEndian.PutUint64(fieldNonce, elem.nonce)

			// pad content to multiple of 16
			paddingSize := calculatePaddingSize(len(elem.packet), int(device.tun.mtu.Load()))
			elem.packet = append(elem.packet, paddingZeros[:paddingSize]...)

			// encrypt content and release to consumer

			binary.LittleEndian.PutUint64(nonce[4:], elem.nonce)
			elem.packet = elem.keypair.send.Seal(
				header,
				nonce[:],
				elem.packet,
				nil,
			)
		}
		elemsContainer.Unlock()
	}
}

func (peer *Peer) RoutineSequentialSender(maxBatchSize int) {
	device := peer.device
	defer func() {
		defer device.Log.Verbosef("%v - Routine: sequential sender - stopped", peer)
		peer.stopping.Done()
	}()
	device.Log.Verbosef("%v - Routine: sequential sender - started", peer)

	bufs := make([][]byte, 0, maxBatchSize)
	eps := make([]conn.Endpoint, 0)

	for elemsContainer := range peer.queue.outbound.c {
		bufs = bufs[:0]
		eps = eps[:0]
		if elemsContainer == nil {
			return
		}
		if !peer.isRunning.Load() {
			// peer has been stopped; return re-usable Elems to the shared pool.
			// This is an optimization only. It is possible for the peer to be stopped
			// immediately after this check, in which case, elem will get processed.
			// The timers and SendBuffers code are resilient to a few stragglers.
			// TODO: rework peer shutdown order to ensure
			// that we never accidentally keep timers alive longer than necessary.
			elemsContainer.Lock()
			for _, elem := range elemsContainer.elems {
				device.PutMessageBuffer(elem.buffer)
				device.PutOutboundElement(elem)
			}
			continue
		}
		dataSent := false
		elemsContainer.Lock()
		for _, elem := range elemsContainer.elems {
			if len(elem.packet) != MessageKeepaliveSize {
				dataSent = true
			}
			bufs = append(bufs, elem.packet)
			eps = append(eps, elem.endpoint)
		}

		peer.timersAnyAuthenticatedPacketTraversal(false)
		peer.timersAnyAuthenticatedPacketSent()

		err := peer.SendBuffers(bufs, eps)
		if dataSent {
			peer.timersDataSent()
		}
		for _, elem := range elemsContainer.elems {
			device.PutMessageBuffer(elem.buffer)
			device.PutOutboundElement(elem)
		}
		device.PutOutboundElementsContainer(elemsContainer)
		if err != nil {
			var errGSO conn.ErrUDPGSODisabled
			if errors.As(err, &errGSO) {
				device.Log.Verbosef(err.Error())
				err = errGSO.RetryErr
			}
		}
		if err != nil {
			device.Log.Errorf("%v - Failed to send data packets: %v", peer, err)
			continue
		}

		peer.keepKeyFreshSending()
	}
}
