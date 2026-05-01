package cmd

import (
	"fmt"
	"os"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

var verifyCmd = &cobra.Command{
	Use:     "verify <config>",
	Short:   "Validate a nylon configuration file",
	Args:    cobra.ExactArgs(1),
	GroupID: "cfg",
	Run: func(cmd *cobra.Command, args []string) {
		isNode, _ := cmd.Flags().GetBool("node")
		data, err := os.ReadFile(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}

		if isNode {
			var cfg state.LocalCfg
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
			if err := state.NodeConfigValidator(&cfg); err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
				os.Exit(1)
			}
		} else {
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
		}

		fmt.Println("Config is valid")
	},
}

func init() {
	rootCmd.AddCommand(verifyCmd)
	verifyCmd.Flags().Bool("node", false, "Validate a node config instead of a central config")
}
