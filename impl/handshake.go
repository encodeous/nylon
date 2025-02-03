package impl

import (
	"errors"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
)

func ConnectCtl(s *state.State, node state.Node) {
	cfg, err := s.GetPubNodeCfg(node)
	if err != nil {
		panic(err)
	}
	go func() {
		for _, addr := range cfg.CtlAddr {
			err := connect(s.Env, cfg, addr)
			if err != nil {
				// ignored, we just cant connect :(
				s.Log.Debug("failed to connect to ctl", "addr", addr, "err", err)
			}
		}
	}()
}

func connect(e *state.Env, cfg state.PubNodeCfg, addr string) error {
	tcp, err := ConnectCtlTCP(addr)
	if err != nil {
		// ignore connection errors, it is expected if we cannot connect to some nodes :)
	} else {
		e.LinkChannel <- &tcp
	}
	return nil
}

func handshake(e *state.Env, link state.CtlLink) (state.PubNodeCfg, error) {
	// TODO actually implement a real handshake
	hello := protocol.HsHello{
		Id: string(e.Id),
	}
	err := link.WriteMsg(&hello)
	if err != nil {
		return state.PubNodeCfg{}, err
	}
	err = link.ReadMsg(&hello)
	if err != nil {
		return state.PubNodeCfg{}, err
	}
	if hello.Id == string(e.Id) {
		// don't connect to ourself!
		return state.PubNodeCfg{}, errors.New("skip connecting to self")
	}
	e.Log.Debug("handshake", "lid", link.Id().String(), "nid", hello.Id)
	return e.GetPubNodeCfg(state.Node(hello.Id))
}

func authenticate(e *state.Env, link state.CtlLink, cfg state.PubNodeCfg) error {
	return nil
}
