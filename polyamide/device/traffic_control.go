package device

import (
	"github.com/encodeous/nylon/polyamide/conn"
	"net/netip"
	"slices"
)

// polyamide traffic control provides a facility to re-order, manipulate, and redirect packets between nylon/polyamide nodes
// this facility operates at the IP/polysock level

type TCAction int
type TCPriority int

const (
	// TcPass will pass the packet on to the next layer
	TcPass TCAction = iota
	// TcBounce will bounce the packet back to the system for handling
	TcBounce
	// TcForward will send the packet through nylon/polyamide. toPeer must be set in TCElement
	TcForward
	// TcDrop will completely drop the packet
	TcDrop
)

const (
	TcNormalPriority TCPriority = iota
	TcMediumPriority
	TcHighPriority
	TcMaxPriority
)

type TCFilter func(dev *Device, packet *TCElement) (TCAction, error)

func TCFAllowedip(dev *Device, packet *TCElement) (TCAction, error) {
	if packet.ToPeer != nil {
		return TcForward, nil
	}
	peer := dev.Allowedips.Lookup(packet.GetDstBytes())
	if peer != nil {
		packet.ToPeer = peer
		return TcForward, nil
	}
	for _, p := range dev.peers.keyMap {
		dev.Allowedips.EntriesForPeer(p, func(prefix netip.Prefix) bool {
			//fmt.Printf("p: %v, prefix: %v\n", p, prefix)
			return true
		})
	}

	//fmt.Printf("nfw addr: %s\n", packet.GetDst().String())
	return TcPass, nil
}

func TCFDrop(dev *Device, packet *TCElement) (TCAction, error) {
	//dev.log.Verbosef("TCFDrop packet: %v", packet)
	return TcDrop, nil
}

func TCFBounce(dev *Device, packet *TCElement) (TCAction, error) {
	if packet.FromPeer != nil {
		//dev.log.Verbosef("TCFBounce packet: %v", packet)
		return TcBounce, nil
	}
	return TcPass, nil
}

type TCElement struct {
	Buffer   *[MaxMessageSize]byte // slice holding the packet data
	Packet   []byte                // slice of "buffer" (always!)
	FromEp   conn.Endpoint         // what the source wireguard UDP endpoint (if any) is
	ToEp     conn.Endpoint         // which wireguard UDP endpoint to send this Packet to
	FromPeer *Peer                 // which peer (if any) sent us this Packet
	ToPeer   *Peer                 // which peer to send this Packet to
	Priority TCPriority            // Priority, higher is better
}

func (elem *TCElement) clearPointers() {
	elem.Buffer = nil
	elem.Packet = nil
	elem.FromEp = nil
	elem.ToEp = nil
	elem.FromPeer = nil
	elem.ToPeer = nil
}

func (device *Device) NewTCElement() *TCElement {
	elem := device.GetTCElement()
	elem.Buffer = device.GetMessageBuffer()
	return elem
}

func (device *Device) InstallFilter(filter TCFilter) {
	device.TCFilters = append(device.TCFilters, filter)
}

type TCState struct {
	priority     [][]*TCElement
	bouncePkts   []*TCElement
	bounceBufs   [][]byte
	elemsForPeer map[*Peer][]*TCElement
}

func NewTCState() *TCState {
	return &TCState{
		priority:     make([][]*TCElement, TcMaxPriority+1),
		bouncePkts:   make([]*TCElement, 0, conn.IdealBatchSize),
		bounceBufs:   make([][]byte, 0, conn.IdealBatchSize),
		elemsForPeer: make(map[*Peer][]*TCElement),
	}
}

func (device *Device) TCBatch(batch []*TCElement, tcs *TCState) {
	for i, elem := range batch {
		// process TC Filters
		act := TcPass
		elem.ParsePacket()
		if !elem.Validate() {
			device.log.Errorf("Found malformed packet, dropping packet")
			act = TcDrop
		} else {
			for _, filter := range slices.Backward(device.TCFilters) {
				nAct, err := filter(device, elem)
				act = nAct
				if err != nil {
					device.log.Errorf("Error on filter action: %v", err)
					act = TcDrop
				}
				if act != TcPass {
					break
				}
			}
		}
		if act == TcPass {
			device.log.Errorf("Unexpectedly passed all filters!")
			act = TcDrop
		}

		batch[i] = nil

		switch act {
		case TcDrop:
			// cleanup
			device.PutMessageBuffer(elem.Buffer)
			device.PutTCElement(elem)
		case TcBounce:
			// bounce back to system
			tcs.bouncePkts = append(tcs.bouncePkts, elem)
		case TcForward:
			// reroute/forward packet
			if elem.ToPeer == nil {
				device.log.Errorf("Failed to forward packet to destination, toPeer not set")
				device.PutMessageBuffer(elem.Buffer)
				device.PutTCElement(elem)
				continue
			}
			tcs.priority[elem.Priority] = append(tcs.priority[elem.Priority], elem)
		default:
			panic("unreachable default case")
		}
	}

	// bounce packets back to the system
	if len(tcs.bouncePkts) > 0 {
		for _, elem := range tcs.bouncePkts {
			tcs.bounceBufs = append(tcs.bounceBufs, elem.Buffer[:MessageTransportHeaderSize+len(elem.Packet)])
		}
		// here, we need to use elem.Buffer instead of elem.Packet since we will get io.ErrShortBuffer if offset < 4
		_, err := device.tun.device.Write(tcs.bounceBufs, MessageTransportHeaderSize)
		if err != nil && !device.isClosed() {
			device.log.Errorf("Failed to loop back packets to TUN device: %v", err)
		}
		for i, elem := range tcs.bouncePkts {
			device.PutMessageBuffer(elem.Buffer)
			device.PutTCElement(elem)
			tcs.bouncePkts[i] = nil
			tcs.bounceBufs[i] = nil
		}
		tcs.bounceBufs = tcs.bounceBufs[:0]
		tcs.bouncePkts = tcs.bouncePkts[:0]
	}

	// forward packets to peers based on priority
	for p, elems := range slices.Backward(tcs.priority) {
		for i, elem := range elems {
			if elem == nil {
				continue
			}
			tcs.elemsForPeer[elem.ToPeer] = append(tcs.elemsForPeer[elem.ToPeer], elem)
			elems[i] = nil
		}
		tcs.priority[p] = tcs.priority[p][:0]
	}

	// stage packets to peers
	for peer, elems := range tcs.elemsForPeer {
		if len(elems) == 0 {
			continue
		}
		if peer.isRunning.Load() {
			obec := device.GetOutboundElementsContainer()
			for i, elem := range elems {
				obe := device.GetOutboundElement()
				obe.nonce = 0
				obe.endpoint = elem.ToEp
				obe.packet = elem.Packet
				obe.buffer = elem.Buffer
				obec.elems = append(obec.elems, obe)
				elems[i] = nil
			}
			peer.StagePackets(obec)
			peer.SendStagedPackets()
		} else {
			for i, elem := range elems {
				device.PutMessageBuffer(elem.Buffer)
				device.PutTCElement(elem)
				elems[i] = nil
			}
		}
		tcs.elemsForPeer[peer] = elems[:0]
	}
}
