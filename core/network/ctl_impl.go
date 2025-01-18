package network

import (
	"encoding/binary"
	"errors"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"io"
	"log/slog"
	"net"
)

type TCPCtlLink struct {
	id   uuid.UUID
	Conn net.Conn
}

func ListenCtlTCP(addr string, links chan<- TCPCtlLink) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			conn.Close()
			slog.Warn("Failed to accept connection", "err", err)
			continue
		}
		links <- TCPCtlLink{uuid.New(), conn}
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
	var length uint32

	err := binary.Read(T.Conn, binary.LittleEndian, &(length))
	if err != nil {
		return err
	}

	if length == 0 || length > MaxPacketSize {
		return errors.New("packet size is invalid")
	}

	data := make([]byte, length)

	_, err = io.ReadFull(T.Conn, data)
	if err != nil {
		return err
	}

	return proto.Unmarshal(data, m)
}

func (T *TCPCtlLink) Id() uuid.UUID {
	return T.id
}

func (T *TCPCtlLink) Metric() uint16 {
	//TODO implement me
	panic("implement me")
}

func (T *TCPCtlLink) SendPacket(m proto.Message) error {
	out, err := proto.Marshal(m)
	if err != nil {
		return err
	}

	if len(out) == 0 || len(out) > MaxPacketSize {
		return errors.New("packet size is invalid")
	}

	var length = uint32(len(out))

	err = binary.Write(T.Conn, binary.LittleEndian, length)
	if err != nil {
		return err
	}

	_, err = T.Conn.Write(out)
	return err
}
