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
	"syscall"
	"time"
)

func Start(ccfg state.CentralCfg, ncfg state.NodeCfg, logLevel slog.Level) error {
	ctx, cancel := context.WithCancelCause(context.Background())

	// create otel environment variables
	if _, ok := os.LookupEnv("OTEL_SERVICE_NAME"); !ok {
		err := os.Setenv("OTEL_SERVICE_NAME", "nylon")
		if err != nil {
			return err
		}
	}

	state.OtelEnabled = false
	//Set up OpenTelemetry.
	//go func() {
	//	_, err := SetupOTelSDK(context.Background())
	//	if err != nil {
	//		slog.Error("opentelemetry setup failed", "err", err)
	//		state.OtelEnabled = false
	//	}
	//}()

	dispatch := make(chan func(env *state.State) error)

	logger := slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level: logLevel,
		//AddSource:    true,

		AddSource:  false,
		TimeFormat: "",

		CustomPrefix: string(ncfg.Id),
	}))

	s := state.State{
		TrustedNodes: make(map[state.Node]ed25519.PublicKey),
		Modules:      make(map[string]state.NyModule),
		Env: &state.Env{
			Context:         ctx,
			Cancel:          cancel,
			DispatchChannel: dispatch,
			CentralCfg:      ccfg,
			NodeCfg:         ncfg,
			Log:             logger,
		},
	}

	for _, node := range ccfg.Nodes {
		s.TrustedNodes[node.Id] = node.PubKey[:]
	}

	s.Log.Info("init modules")
	err := initModules(&s)
	if err != nil {
		return err
	}
	s.Log.Info("init modules complete")

	s.Log.Info("Nylon has been initialized. To gracefully exit, send SIGINT or Ctrl+C.")

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for _ = range c {
			s.Cancel(errors.New("received shutdown signal"))
			break
		}
	}()

	return MainLoop(&s, dispatch)
}

func initModules(s *state.State) error {
	modules := []state.NyModule{
		&impl.Nylon{}, // nylon must start before router
		&impl.Router{},
	}

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
	for {
		select {
		case fun := <-dispatch:
			//s.Log.Debug("start")
			start := time.Now()
			err := fun(s)
			if err != nil {
				s.Log.Error("error occurred during dispatch: ", "error", err)
				s.Cancel(err)
			}
			elapsed := time.Since(start)
			if elapsed > time.Millisecond*50 {
				s.Log.Warn("dispatch took a long time!", "fun", fun, "elapsed", elapsed)
			}
			//s.Log.Debug("done", "elapsed", elapsed)
		case <-s.Context.Done():
			goto endLoop
		}
	}
endLoop:
	s.Log.Info("stopped main loop", "reason", context.Cause(s.Context).Error())
	cleanup(s)
	return nil
}

func cleanup(s *state.State) {
	if s.DispatchChannel != nil {
		close(s.DispatchChannel)
	}
	s.Log.Info("cleaning up modules")
	for moduleName, module := range s.Modules {
		err := module.Cleanup(s)
		if err != nil {
			s.Log.Error("error occurred during cleanup: ", "module", moduleName, "error", err)
		}
	}
	s.Cancel(context.Canceled)
}
