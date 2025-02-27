package cmd

import (
	"bytes"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"os"
)

// joinCmd represents the node command
var bundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Bundles the current central configuration, ready for distribution across nodes",
	Run: func(cmd *cobra.Command, args []string) {
		cfgFile, err := os.ReadFile(state.CentralConfigPath)
		if err != nil {
			panic(err)
		}
		keyFile, err := os.ReadFile(state.CentralKeyPath)
		if err != nil {
			panic(err)
		}
		key := &state.NyPrivateKey{}
		err = key.UnmarshalText(bytes.TrimSpace(keyFile))
		if err != nil {
			panic(err)
		}
		bundle, err := state.BundleConfig(string(cfgFile), *key)
		if err != nil {
			panic(err)
		}

		err = os.WriteFile("central.nybundle", []byte(bundle), 0700)
		if err != nil {
			panic(err)
		}
		println("Wrote bundle to central.nybundle")
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(bundleCmd)
}
