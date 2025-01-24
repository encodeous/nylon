package udp_link

import (
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
)

func ConnectCtl(s *state.State, node state.Node) {
	cfg, err := s.GetPubNodeCfg(node)
	if err != nil {
		panic(err)
	}
	go func() {
		err := connect(s.Env, cfg)
		if err != nil {
			s.Env.Cancel(err)
		}
	}()
}

func connect(e *state.Env, cfg state.PubNodeCfg) error {
	tcp, err := ConnectCtlTCP(cfg.CtlAddr)
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
	return e.GetPubNodeCfg(state.Node(hello.Id))
}

func authenticate(e *state.Env, link state.CtlLink, cfg state.PubNodeCfg) error {
	return nil
}
