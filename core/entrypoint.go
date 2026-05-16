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

// Bootstrap provides startup logic in a real environment
func Bootstrap(centralPath, nodePath, logPath string, verbose bool, opts state.NylonOptions) {
	setupDebugging(opts)
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

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
	err = state.NodeConfigValidator(centralCfg, nodeCfg)
	if err != nil {
		panic(err)
	}
	n, err := NewNylon(*centralCfg, *nodeCfg, level, centralPath, nil, opts, nil)
	if err != nil {
		panic(err)
	}
	err = n.Start()
	if err != nil {
		panic(err)
	}
}
