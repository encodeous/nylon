package device

import (
	"slices"

	"github.com/encodeous/nylon/polyamide/conn"
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
		//fmt.Printf("fw: %s -> %s\n", packet.GetDst().String(), peer)
		return TcForward, nil
	}

	//fmt.Printf("nfw addr: %s\n", packet.GetDst().String())
	return TcPass, nil
}

func TCFDrop(dev *Device, packet *TCElement) (TCAction, error) {
	//dev.Log.Verbosef("TCFDrop packet: %v -> %v", packet.GetSrc(), packet.GetDst())
	return TcDrop, nil
}

func TCFBounce(dev *Device, packet *TCElement) (TCAction, error) {
	if packet.Incoming() {
		//dev.Log.Verbosef("TCFBounce packet: %v -> %v", packet.GetSrc(), packet.GetDst())
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

func (device *Device) TCProcess(elem *TCElement) {
	// process TC Filters
	act := TcPass
	elem.ParsePacket()
	if !elem.Validate() {
		device.Log.Errorf("Found malformed packet, dropping packet")
		act = TcDrop
	} else {
		for _, filter := range slices.Backward(device.TCFilters) {
			nAct, err := filter(device, elem)
			act = nAct
			if err != nil {
				device.Log.Errorf("Error on filter action: %v", err)
				act = TcDrop
			}
			if act != TcPass {
				break
			}
		}
	}
	if act == TcPass {
		device.Log.Errorf("Unexpectedly passed all filters!")
		act = TcDrop
	}

	switch act {
	case TcDrop:
		// cleanup
		device.PutMessageBuffer(elem.Buffer)
		device.PutTCElement(elem)
	case TcBounce:
		// bounce back to system
		buf := elem.Buffer[:MessageTransportHeaderSize+len(elem.Packet)]
		// here, we need to use elem.Buffer instead of elem.Packet since we will get io.ErrShortBuffer if offset < 4
		_, err := device.tun.device.Write([][]byte{buf}, MessageTransportHeaderSize)
		if err != nil && !device.isClosed() {
			device.Log.Errorf("Failed to loop back packet to TUN device: %v", err)
		}
		device.PutMessageBuffer(elem.Buffer)
		device.PutTCElement(elem)
	case TcForward:
		// reroute/forward packet
		if elem.ToPeer == nil {
			device.Log.Errorf("Failed to forward packet to destination, toPeer not set")
			device.PutMessageBuffer(elem.Buffer)
			device.PutTCElement(elem)
			return
		}

		peer := elem.ToPeer
		if peer.isRunning.Load() {
			obec := device.GetOutboundElementsContainer()
			obe := device.GetOutboundElement()
			obe.nonce = 0
			obe.endpoint = elem.ToEp
			obe.packet = elem.Packet
			obe.buffer = elem.Buffer
			obec.elems = append(obec.elems, obe)
			device.PutTCElement(elem)
			peer.StagePackets(obec)
			peer.SendStagedPackets()
		} else {
			device.PutMessageBuffer(elem.Buffer)
			device.PutTCElement(elem)
		}
	default:
		panic("unreachable default case")
	}
}
