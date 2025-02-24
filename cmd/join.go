package cmd

import (
	"gopkg.in/yaml.v3"
	"os"

	"github.com/spf13/cobra"
)

// joinCmd represents the node command
var joinCmd = &cobra.Command{
	Use:   "join",
	Short: "Add the current node to an existing Nylon network",
	Run: func(cmd *cobra.Command, args []string) {
		nodeCfg := promptCreateNode()

		ncfg, err := yaml.Marshal(&nodeCfg)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(nodeConfigPath, ncfg, 0700)
		if err != nil {
			panic(err)
		}
	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(joinCmd)
}
