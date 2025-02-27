package cmd

import (
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"os"
)

var bundlePath string

// joinCmd represents the node command
var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Bundles provided bundle against the public key",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 || args[0] == "" {
			panic("expecting argument public key")
		}
		pkey := &state.NyPublicKey{}
		err := pkey.UnmarshalText([]byte(args[0]))
		if err != nil {
			panic(err)
		}

		bundleStr, err := os.ReadFile(bundlePath)
		if err != nil {
			panic(err)
		}
		config, err := state.UnbundleConfig(string(bundleStr), *pkey)
		if err != nil {
			panic(err)
		}

		cfgYaml, err := yaml.Marshal(config)
		if err != nil {
			panic(err)
		}

		println("Bundle is valid")
		println(string(cfgYaml))
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(verifyCmd)

	verifyCmd.Flags().StringVarP(&bundlePath, "bundle", "b", "central.nybundle", "Path to bundle file")
}
