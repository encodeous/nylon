package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"os"
)

// netCmd represents the net command
var netCmd = &cobra.Command{
	Use:   "new-net",
	Short: "Create a new nylon network with central configuration",
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

		pkey := state.GenerateKey()

		centralConfig := state.CentralCfg{
			RootPubKey: pkey.XPubkey(),
			Nodes: []state.PubNodeCfg{
				promptGenPubCfg(nodeCfg),
			},
			Graph: []string{
				"Group1 = node1, node2",
				"Group2 = node5, node6",
				"Group1, Group2, node7",
			},
			Version: 0,
		}

		fmt.Println("\n\nCentral Network Configuration")

		centralConfigPath = safeSaveFile(centralConfigPath, "Central Config")
		centralKeyPath = safeSaveFile(centralKeyPath, "Central Key")
		ccfg, err := yaml.Marshal(&centralConfig)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(centralConfigPath, ccfg, 0700)
		if err != nil {
			panic(err)
		}

		key, err := pkey.MarshalText()
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(centralKeyPath, key, 0700)
		if err != nil {
			panic(err)
		}

	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(netCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// netCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// netCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
