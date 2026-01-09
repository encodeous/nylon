package core

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path"
	"reflect"
	"runtime"
	"runtime/trace"
	"syscall"
	"time"

	"github.com/encodeous/nylon/perf"
	"github.com/encodeous/nylon/state"
	"github.com/encodeous/tint"
	"github.com/goccy/go-yaml"
	slogmulti "github.com/samber/slog-multi"
)

func setupDebugging() {
	if state.DBG_trace {
		f, err := os.Create("trace.out")
		if err != nil {
			log.Fatal(err)
		}
		err = trace.Start(f)
		defer trace.Stop()
		if err != nil {
			return
		}
		log.Println("Started tracing")
	}
	if state.DBG_debug {
		go func() {
			log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
		}()
	}
}

func readCentralConfig(centralPath, nodePath string) (*state.CentralCfg, error) {
	var centralCfg state.CentralCfg

	file, err := os.ReadFile(centralPath)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		// fallback to using dist from node config

		var nodeCfg state.LocalCfg

		file, err = os.ReadFile(nodePath)
		if err != nil {
			return nil, fmt.Errorf("central.yaml not found and failed to read node.yaml: %w", err)
		}

		err = yaml.Unmarshal(file, &nodeCfg)
		if err != nil {
			return nil, err
		}

		if nodeCfg.Dist == nil {
			return nil, fmt.Errorf("central.yaml not found and node.yaml has no dist config")
		}

		cfg, err := FetchConfig(nodeCfg.Dist.Url, nodeCfg.Dist.Key)
		if err != nil {
			return nil, err
		}

		bytes, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		err = os.WriteFile(centralPath, bytes, 0700)
		if err != nil {
			return nil, err
		}

		centralCfg = *cfg
	} else {
		err = yaml.Unmarshal(file, &centralCfg)
		if err != nil {
			return nil, err
		}
	}
	return &centralCfg, nil
}

func readNodeConfig(nodePath string) (*state.LocalCfg, error) {
	var nodeCfg state.LocalCfg
	file, err := os.ReadFile(nodePath)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(file, &nodeCfg)
	if err != nil {
		return nil, err
	}
	return &nodeCfg, nil
}

// Bootstrap manages the lifetime of the whole application. Nylon may be restarted multiple times, but Bootstrap is only called once.
func Bootstrap(centralPath, nodePath, logPath string, verbose bool) {
	setupDebugging()
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	for {
		centralCfg, err := readCentralConfig(centralPath, nodePath)
		if err != nil {
			panic(err)
		}
		nodeCfg, err := readNodeConfig(nodePath)
		if err != nil {
			panic(err)
		}
		if logPath != "" {
			nodeCfg.LogPath = logPath
		}

		state.ExpandCentralConfig(centralCfg)
		err = state.CentralConfigValidator(centralCfg)
		if err != nil {
			panic(err)
		}
		err = state.NodeConfigValidator(nodeCfg)
		if err != nil {
			panic(err)
		}
		restart, err := Start(*centralCfg, *nodeCfg, level, centralPath, nil, nil)
		if err != nil {
			panic(err)
		}
		if !restart {
			break
		}
	}
}

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
	modules = append(modules, &NylonRouter{})
	modules = append(modules, &Nylon{})

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
			perf.DispatchLatency.Add(float64(elapsed.Microseconds()))
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
