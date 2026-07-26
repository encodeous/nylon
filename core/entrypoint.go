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

func readCentralConfig(centralPath, nodePath string, tunables *state.RouterTunables) (*state.CentralCfg, error) {
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

		cfg, err := FetchConfig(nodeCfg.Dist.Url, nodeCfg.Dist.Key, tunables.MaxConfigSize)
		if err != nil {
			return nil, err
		}

		bytes, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		err = os.WriteFile(centralPath, bytes, 0600)
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

	tunables := state.DefaultRouterTunables()
	centralCfg, err := readCentralConfig(centralPath, nodePath, &tunables)
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
	if opts.AdvertiseExitNodeSet {
		nodeCfg.AdvertiseExitNode = opts.AdvertiseExitNode
	}
	if opts.ExitNodeSet {
		nodeCfg.ExitNode = opts.ExitNode
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
