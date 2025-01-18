package impl

import (
	"encoding/binary"
	"errors"
	"github.com/encodeous/nylon/protocol"
	state2 "github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"reflect"
)

func AddSeqno(a, b uint16) uint16 {
	if a == INF || b == INF {
		return INF
	} else {
		return min(INF-1, a+b)
	}
}

func SeqnoLt(a, b uint16) bool {
	x := (b - a) % 63336
	return 0 < x && x < 32768
}

func IsFeasible(curRoute state2.Route, newRoute state2.PubRoute, metric uint16) bool {
	if SeqnoLt(newRoute.Src.Seqno, curRoute.Src.Seqno) {
		return false
	}

	if metric < curRoute.Fd ||
		SeqnoLt(curRoute.Src.Seqno, newRoute.Src.Seqno) ||
		(metric == curRoute.Fd && curRoute.Metric == INF) {
		return true
	}
	return false
}

func ReceivePacket(c net.Conn, m proto.Message) error {
	var length uint32

	err := binary.Read(c, binary.LittleEndian, &(length))
	if err != nil {
		return err
	}

	if length == 0 || length > protocol.MaxPacketSize {
		return errors.New("packet size is invalid")
	}

	data := make([]byte, length)

	_, err = io.ReadFull(c, data)
	if err != nil {
		return err
	}

	return proto.Unmarshal(data, m)
}

func SendPacket(c net.Conn, m proto.Message) error {
	out, err := proto.Marshal(m)
	if err != nil {
		return err
	}

	if len(out) == 0 || len(out) > protocol.MaxPacketSize {
		return errors.New("packet size is invalid")
	}

	var length = uint32(len(out))

	err = binary.Write(c, binary.LittleEndian, length)
	if err != nil {
		return err
	}

	_, err = c.Write(out)
	return err
}

func Get[T state2.NyModule](s state2.State) T {
	t := reflect.TypeFor[T]()
	return s.Modules[t.String()].(T)
}
