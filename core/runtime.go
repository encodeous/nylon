package core

import (
	"context"
	"errors"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/tint"
	slogmulti "github.com/samber/slog-multi"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"reflect"
	"runtime"
	"syscall"
	"time"
)

func Start(ccfg state.CentralCfg, ncfg state.LocalCfg, logLevel slog.Level, configPath string, aux map[string]any, initState **state.State) (bool, error) {
	ctx, cancel := context.WithCancelCause(context.Background())

	dispatch := make(chan func(env *state.State) error, 128)

	handlers := make([]slog.Handler, 0)
	handlers = append(handlers,
		tint.NewHandler(os.Stderr, &tint.Options{
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

	if ncfg.LogPath != "" {
		err := os.MkdirAll(path.Dir(ncfg.LogPath), 0700)
		if err != nil {
			return false, err
		}
		f, err := os.OpenFile(ncfg.LogPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0700)
		if err != nil {
			return false, err
		}
		handlers = append(handlers, slog.NewTextHandler(f, &slog.HandlerOptions{Level: logLevel}))
	}

	logger := slog.New(
		slogmulti.Fanout(handlers...))

	if ncfg.InterfaceName == "" {
		ncfg.InterfaceName = "nylon"
	}

	s := state.State{
		Modules: make(map[string]state.NyModule),
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
	modules = append(modules, &Nylon{}) // nylon must start before router
	modules = append(modules, &Router{})

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
				s.Log.Warn("dispatch took a long time!", "fun", runtime.FuncForPC(reflect.ValueOf(fun).Pointer()).Name(), "elapsed", elapsed, "len", len(dispatch))
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
