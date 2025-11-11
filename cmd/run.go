package cmd

import (
	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
)

import _ "net/http/pprof" // remove in stable version of nylon

// runCmd represents the run command
var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run nylon",
	Long:  `This will run nylon`,
	Run: func(cmd *cobra.Command, args []string) {
		centralPath := cmd.Flag("config").Value.String()
		nodePath := cmd.Flag("node").Value.String()
		logPath := cmd.Flag("log").Value.String()

		isVerbose := false
		if ok, _ := cmd.Flags().GetBool("verbose"); ok {
			isVerbose = true
		}

		core.Bootstrap(centralPath, nodePath, logPath, isVerbose)
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(runCmd)

	runCmd.Flags().BoolP("verbose", "v", false, "Verbose output")
	runCmd.Flags().BoolVarP(&state.DBG_log_probe, "dbg-probe", "p", false, "Write probes to console")
	runCmd.Flags().BoolVarP(&state.DBG_log_wireguard, "dbg-wg", "w", false, "Outputs wireguard logs to the console")
	runCmd.Flags().BoolVarP(&state.DBG_log_repo_updates, "dbg-repo", "", false, "Outputs repo updates to the console")
	runCmd.Flags().BoolVarP(&state.DBG_debug, "dbg-perf", "", false, "Enables performance debugging server on port 6060")
	runCmd.Flags().BoolVarP(&state.DBG_trace, "dbg-trace", "", false, "Enables trace to trace.out")
	runCmd.Flags().StringP("config", "c", DefaultConfigPath, "Path to the config file")
	runCmd.Flags().StringP("node", "n", DefaultNodeConfigPath, "Path to the node config file")
	runCmd.Flags().StringP("log", "l", "", "Path to the log file (overrides config)")
}
