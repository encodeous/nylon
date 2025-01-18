package core

import (
	"context"
	"crypto/ed25519"
	impl2 "github.com/encodeous/nylon/impl"
	state2 "github.com/encodeous/nylon/state"
	"github.com/encodeous/tint"
	"log/slog"
	"os"
	"reflect"
	"time"
)

func Start(ccfg state2.CentralCfg, ncfg state2.NodeCfg, logLevel slog.Level) error {
	ctx, cancel := context.WithCancelCause(context.Background())

	dispatch := make(chan func(env state2.State) error)

	logger := slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:        logLevel,
		AddSource:    true,
		CustomPrefix: string(ncfg.Id),
	}))

	s := state2.State{
		TrustedNodes: make(map[state2.Node]ed25519.PublicKey),
		Modules:      make(map[string]state2.NyModule),
		Env: state2.Env{
			Context:         ctx,
			Cancel:          cancel,
			DispatchChannel: dispatch,
			CCfg:            ccfg,
			NCfg:            ncfg,
			Log:             logger,
		},
	}

	for _, node := range ccfg.Nodes {
		s.TrustedNodes[node.Id] = ed25519.PublicKey(node.PubKey)
	}

	s.Log.Info("init modules")
	err := initModules(s)
	if err != nil {
		return err
	}
	s.Log.Info("init modules complete")

	return MainLoop(s, dispatch)
}

func initModules(s state2.State) error {
	modules := []state2.NyModule{
		&impl2.Router{},
		&impl2.Nylon{},
	}

	for _, module := range modules {
		s.Modules[reflect.TypeOf(module).String()] = module
		if err := module.Init(s); err != nil {
			return err
		}
	}
	return nil
}

func MainLoop(s state2.State, dispatch <-chan func(state2.State) error) error {
	s.Log.Debug("started main loop")
	for {
		select {
		case fun := <-dispatch:
			s.Log.Debug("start")
			start := time.Now()
			err := fun(s)
			if err != nil {
				return err
			}
			elapsed := time.Since(start)
			s.Log.Debug("done", "elapsed", elapsed)
		case <-s.Context.Done():
			goto endLoop
		}
	}
endLoop:
	s.Log.Info("stopped main loop")
	cleanup(s)
	return nil
}

func cleanup(s state2.State) {
	close(s.LinkChannel)
	close(s.DispatchChannel)
	s.Cancel(context.Canceled)
}
