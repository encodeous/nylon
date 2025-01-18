package impl

import (
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"net"
)

type TCPCtlLink struct {
	id   uuid.UUID
	Conn net.Conn
}

func ListenCtlTCP(e state.Env, addr string) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		e.Log.Error("Failed to listen on addr", "addr", addr, "err", err)
		e.Dispatch(func(env state.State) error {
			e.Cancel(err)
			return nil
		})
		return
	}
	e.Log.Info("Listening on", "addr", addr)
	for {
		conn, err := listener.Accept()
		if err != nil {
			conn.Close()
			e.Log.Warn("Failed to accept connection", "err", err)
			continue
		}
		e.LinkChannel <- &TCPCtlLink{uuid.New(), conn}
	}
}

func ConnectCtlTCP(addr string) (TCPCtlLink, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return TCPCtlLink{}, err
	}
	return TCPCtlLink{uuid.New(), conn}, nil
}

func (T *TCPCtlLink) ReceivePacket(m proto.Message) error {
	return ReceivePacket(T.Conn, m)
}

func (T *TCPCtlLink) Id() uuid.UUID {
	return T.id
}

func (T *TCPCtlLink) Metric() uint16 {
	//TODO implement me
	panic("implement me")
}

func (T *TCPCtlLink) SendPacket(m proto.Message) error {
	return SendPacket(T.Conn, m)
}
