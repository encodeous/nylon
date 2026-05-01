package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "nylon",
	Short: "Nylon CLI",
	Long: `Nylon is a mesh networking system, designed to provide secure, reliable, and high-performance connectivity for distributed systems.

Documentation: https://nylon.jq.ax
GitHub: https://github.com/encodeous/nylon`,
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
