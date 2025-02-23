package impl

import (
	"github.com/encodeous/nylon/state"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"net"
	"net/netip"
	"sync"
)

type TCPCtlLink struct {
	id     uuid.UUID
	Conn   net.Conn
	remote bool
	mutex  sync.Mutex
	dead   bool
}

func (T *TCPCtlLink) IsActive() bool {
	return !T.dead
}

func (T *TCPCtlLink) IsAlive() bool {
	return !T.dead
}

func (T *TCPCtlLink) Close() {
	T.dead = true
	T.Conn.Close()
}

func (T *TCPCtlLink) IsRemote() bool {
	return T.remote
}

func ListenCtlTCP(e *state.Env, addr netip.AddrPort) {
	config := net.ListenConfig{}
	listener, err := config.Listen(e.Context, "tcp", addr.String())
	if err != nil {
		e.Log.Error("Failed to listen on addr", "addr", addr, "err", err)
		e.Dispatch(func(env *state.State) error {
			e.Cancel(err)
			return nil
		})
		return
	}

	e.Log.Info("listening on", "addr", addr)
	for e.Context.Err() == nil {
		conn, err := listener.Accept()
		if err != nil {
			conn.Close()
			e.Log.Warn("Failed to accept connection", "err", err)
			continue
		}
		e.LinkChannel <- &TCPCtlLink{uuid.New(), conn, true, sync.Mutex{}, false}
	}
}

func ConnectCtlTCP(addr string) (TCPCtlLink, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return TCPCtlLink{}, err
	}
	return TCPCtlLink{uuid.New(), conn, false, sync.Mutex{}, false}, nil
}

func (T *TCPCtlLink) ReadMsg(m proto.Message) error {
	return receive(T.Conn, m)
}

func (T *TCPCtlLink) Id() uuid.UUID {
	return T.id
}

func (T *TCPCtlLink) Metric() uint16 {
	//TODO implement me
	panic("implement me")
}

func (T *TCPCtlLink) WriteMsg(m proto.Message) error {
	T.mutex.Lock()
	defer T.mutex.Unlock()
	return send(T.Conn, m)
}
