package cmd

import (
	"github.com/spf13/cobra"
	"os"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "nylon",
	Short: "Nylon Distributed Networking CLI",
	Long: `Nylon is a distributed mesh networking system.
At its core, nylon ensures nodes are reachable even under the most difficult network conditions, without compromising performance or security.`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

var nodeConfigPath = "./nylon-node.yaml"
var centralConfigPath = "./nylon-central.yaml"
var centralKeyPath = "./nylon-central.key"

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.AddGroup(&cobra.Group{
		ID:    "init",
		Title: "Initialize Nylon",
	})
	rootCmd.AddGroup(&cobra.Group{
		ID:    "ny",
		Title: "Nylon Commands",
	})
	rootCmd.PersistentFlags().StringVarP(&nodeConfigPath, "node-config", "n", nodeConfigPath, "node-specific config")
	rootCmd.PersistentFlags().StringVarP(&centralConfigPath, "central-config", "c", centralConfigPath, "network-global config")
	rootCmd.PersistentFlags().StringVarP(&centralKeyPath, "central-key", "k", centralKeyPath, "network-global administration key")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	//rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
