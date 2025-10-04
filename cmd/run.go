package cmd

import (
	"log"
	"log/slog"
	"net/http"
	"os"
	"runtime/trace"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

import _ "net/http/pprof" // remove in stable version of nylon

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run nylon",
	Long:  `This will run nylon`,
	Run: func(cmd *cobra.Command, args []string) {
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
		if state.DBG_pprof {
			go func() {
				log.Println(http.ListenAndServe("0.0.0.0:6060", nil))
			}()
		}

		centralPath := cmd.Flag("config").Value.String()
		nodePath := cmd.Flag("node").Value.String()
		logPath := cmd.Flag("log").Value.String()
	start:
		var centralCfg state.CentralCfg
		file, err := os.ReadFile(centralPath)
		if err != nil {
			panic(err)
		}
		err = yaml.Unmarshal(file, &centralCfg)
		if err != nil {
			panic(err)
		}

		var nodeCfg state.LocalCfg
		file, err = os.ReadFile(nodePath)
		if err != nil {
			panic(err)
		}
		err = yaml.Unmarshal(file, &nodeCfg)
		if err != nil {
			panic(err)
		}

		if logPath != "" {
			nodeCfg.LogPath = logPath
		}

		err = state.CentralConfigValidator(&centralCfg)
		if err != nil {
			panic(err)
		}
		state.ExpandCentralConfig(&centralCfg)
		err = state.NodeConfigValidator(&nodeCfg)
		if err != nil {
			panic(err)
		}

		level := slog.LevelInfo
		if ok, _ := cmd.Flags().GetBool("verbose"); ok {
			level = slog.LevelDebug
		}

		restart, err := core.Start(centralCfg, nodeCfg, level, centralPath, nil, nil)
		if err != nil {
			panic(err)
		}
		if restart {
			goto start
		}
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().BoolP("verbose", "v", false, "Verbose output")
	runCmd.Flags().BoolVarP(&state.DBG_log_probe, "dbg-probe", "p", false, "Write probes to console")
	runCmd.Flags().BoolVarP(&state.DBG_log_wireguard, "dbg-wg", "w", false, "Outputs wireguard logs to the console")
	runCmd.Flags().BoolVarP(&state.DBG_log_repo_updates, "dbg-repo", "", false, "Outputs repo updates to the console")
	runCmd.Flags().BoolVarP(&state.DBG_pprof, "dbg-pprof", "", false, "Enables pprof on port 6060")
	runCmd.Flags().BoolVarP(&state.DBG_trace, "dbg-trace", "", false, "Enables trace to trace.out")
	runCmd.Flags().StringP("config", "c", DefaultConfigPath, "Path to the config file")
	runCmd.Flags().StringP("node", "n", DefaultNodeConfigPath, "Path to the node config file")
	runCmd.Flags().StringP("log", "l", "", "Path to the log file (overrides config)")
}
