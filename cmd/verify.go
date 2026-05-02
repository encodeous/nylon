package cmd

import (
	"fmt"
	"os"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:     "verify <central-config>",
	Short:   "Validate nylon configuration files",
	Args:    cobra.ExactArgs(1),
	GroupID: "cfg",
	Run: func(cmd *cobra.Command, args []string) {
		data, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		var cfg state.CentralCfg
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		state.ExpandCentralConfig(&cfg)
		if err := state.CentralConfigValidator(&cfg); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		var ncfg state.LocalCfg
		nodePath, _ := cmd.Flags().GetString("node")
		if nodePath != "" {
			nData, err := os.ReadFile(nodePath)
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
			if err := yaml.Unmarshal(nData, &ncfg); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
			if err := state.NodeConfigValidator(&cfg, &ncfg); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		}

		fmt.Println("Config is valid")
	},
}

func init() {
	rootCmd.AddCommand(verifyCmd)
	verifyCmd.Flags().String("node", "", "Also validate the node config")
}
