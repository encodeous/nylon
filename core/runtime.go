package core

import (
	"context"
	"crypto/ed25519"
	"errors"
	"github.com/encodeous/nylon/impl"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/tint"
	"log/slog"
	"os"
	"os/signal"
	"reflect"
	"runtime"
	"syscall"
	"time"
)

func Start(ccfg state.CentralCfg, ncfg state.LocalCfg, logLevel slog.Level, configPath string, aux map[string]any, initState **state.State) (bool, error) {
	ctx, cancel := context.WithCancelCause(context.Background())

	dispatch := make(chan func(env *state.State) error, 512)

	logger := slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:        logLevel,
		AddSource:    false,
		CustomPrefix: string(ncfg.Id),
		ReplaceAttr: func(groups []string, attr slog.Attr) slog.Attr {
			if attr.Key == "time" {
				return slog.Attr{}
			}
			return attr
		},
	}))

	s := state.State{
		TrustedNodes: make(map[state.NodeId]ed25519.PublicKey),
		Modules:      make(map[string]state.NyModule),
		Env: &state.Env{
			Context:         ctx,
			Cancel:          cancel,
			DispatchChannel: dispatch,
			CentralCfg:      ccfg,
			LocalCfg:        ncfg,
			Log:             logger,
			ConfigPath:      configPath,
			AuxConfig:       aux,
		},
	}
	if initState != nil {
		*initState = &s
	}

	for _, node := range ccfg.Routers {
		s.TrustedNodes[node.Id] = node.PubKey[:]
	}

	s.Log.Info("init modules")
	err := initModules(&s)
	if err != nil {
		return false, err
	}
	s.Log.Info("init modules complete")

	s.Log.Info("Nylon has been initialized. To gracefully exit, send SIGINT or Ctrl+C.")

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case _ = <-c:
			s.Cancel(errors.New("received shutdown signal"))
		case <-ctx.Done():
			return
		}
	}()

	err = MainLoop(&s, dispatch)
	if err != nil {
		return false, err
	}
	if s.Updating.Load() {
		s.Log.Info("Restarting Nylon...")
		return true, nil
	}
	return false, nil
}

func initModules(s *state.State) error {
	var modules []state.NyModule
	modules = append(modules, &impl.Nylon{}) // nylon must start before router
	modules = append(modules, &impl.Router{})

	for _, module := range modules {
		s.Modules[reflect.TypeOf(module).String()] = module
		if err := module.Init(s); err != nil {
			return err
		}
	}
	return nil
}

func MainLoop(s *state.State, dispatch <-chan func(*state.State) error) error {
	s.Log.Debug("started main loop")
	s.Started.Store(true)
	for {
		select {
		case fun := <-dispatch:
			if fun == nil {
				goto endLoop
			}
			//s.Log.Debug("start")
			start := time.Now()
			err := fun(s)
			if err != nil {
				s.Log.Error("error occurred during dispatch: ", "error", err)
				s.Cancel(err)
			}
			elapsed := time.Since(start)
			if elapsed > time.Millisecond*4 {
				s.Log.Warn("dispatch took a long time!", "fun", runtime.FuncForPC(reflect.ValueOf(fun).Pointer()).Name(), "elapsed", elapsed)
			}
			//s.Log.Debug("done", "elapsed", elapsed)
		case <-s.Context.Done():
			goto endLoop
		}
	}
endLoop:
	s.Log.Info("stopped main loop", "reason", context.Cause(s.Context).Error())
	Stop(s)
	return nil
}

func Stop(s *state.State) {
	if s.Stopping.Swap(true) {
		return // don't stop twice
	}
	s.Cancel(context.Canceled)
	if s.DispatchChannel != nil {
		close(s.DispatchChannel)
		s.DispatchChannel = nil
	}
	s.Log.Info("cleaning up modules")
	for moduleName, module := range s.Modules {
		err := module.Cleanup(s)
		if err != nil {
			s.Log.Error("error occurred during Stop: ", "module", moduleName, "error", err)
		}
	}
	s.Log.Info("stopped")
}
