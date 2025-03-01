package cmd

import (
	"github.com/spf13/cobra"
	"os"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "nylon",
	Short: "Nylon CLI",
	Long: `Nylon is a mesh networking system.
At its core, nylon ensures nodes are reachable even under the most difficult network conditions, without compromising performance or security.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddGroup(&cobra.Group{
		ID:    "init",
		Title: "Initialize Nylon",
	})
	rootCmd.AddGroup(&cobra.Group{
		ID:    "ny",
		Title: "Nylon Commands",
	})
	rootCmd.AddGroup(&cobra.Group{
		ID:    "cfg",
		Title: "Config Commands",
	})
}
