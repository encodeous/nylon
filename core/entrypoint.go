package core

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"runtime/trace"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

func setupDebugging(opts state.NylonOptions) {
	if opts.DBG_trace {
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
	if opts.DBG_debug {
		go func() {
			log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
		}()
	}
}

func readCentralConfig(centralPath string) (*state.CentralCfg, error) {
	var centralCfg state.CentralCfg

	file, err := os.ReadFile(centralPath)
	if err != nil {
		return nil, err
	}
	if err = yaml.Unmarshal(file, &centralCfg); err != nil {
		return nil, err
	}
	return &centralCfg, nil
}

func readNodeConfig(nodePath string) (*state.LocalCfg, error) {
	file, err := os.ReadFile(nodePath)
	if err != nil {
		return nil, err
	}
	return state.ParseLocalConfig(file)
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "Error: %s: %v\n", msg, err)
	os.Exit(1)
}

// Bootstrap provides startup logic in a real environment
func Bootstrap(centralPath, nodePath, logPath string, verbose bool, opts state.NylonOptions) {
	setupDebugging(opts)
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	centralCfg, err := readCentralConfig(centralPath)
	if err != nil {
		fatal("failed to read central config", err)
	}
	nodeCfg, err := readNodeConfig(nodePath)
	if err != nil {
		fatal("failed to read node config", err)
	}
	if logPath != "" {
		nodeCfg.LogPath = logPath
	}

	state.ExpandCentralConfig(centralCfg)
	if err = state.CentralConfigValidator(centralCfg); err != nil {
		fatal("invalid central config", err)
	}
	if err = state.NodeConfigValidator(centralCfg, nodeCfg); err != nil {
		fatal("invalid node config", err)
	}
	n, err := NewNylon(*centralCfg, *nodeCfg, level, centralPath, nil, opts, nil)
	if err != nil {
		fatal("failed to initialize nylon", err)
	}
	if err = n.Start(); err != nil {
		fatal("nylon exited with error", err)
	}
}
