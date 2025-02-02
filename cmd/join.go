package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
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

		var centralCfg state.CentralCfg
		file, err := os.ReadFile(centralConfigPath)
		if err != nil {
			panic(err)
		}
		err = yaml.Unmarshal(file, &centralCfg)
		if err != nil {
			panic(err)
		}

		centralCfg.Nodes = append(centralCfg.Nodes, promptGenPubCfg(nodeCfg))

		ccfg, err := yaml.Marshal(&centralCfg)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(centralConfigPath, ccfg, 0700)
		if err != nil {
			panic(err)
		}

		fmt.Println("Central Config has been written to disk, please sync this with the rest of the network.")
	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(joinCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// joinCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// joinCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
