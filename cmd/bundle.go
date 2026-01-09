package cmd

import (
	"bytes"
	"fmt"
	"os"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
)

var sealCmd = &cobra.Command{
	Use:   "seal",
	Short: "Bundles the provided central configuration, ready for distribution across nodes",
	Run: func(cmd *cobra.Command, args []string) {
		cfgPath := cmd.Flag("config").Value.String()
		keyPath := cmd.Flag("key").Value.String()
		outPath := cmd.Flag("output").Value.String()
		cfgFile, err := os.ReadFile(cfgPath)
		if err != nil {
			panic(err)
		}
		keyFile, err := os.ReadFile(keyPath)
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

		err = os.WriteFile(outPath, []byte(bundle), 0600)
		if err != nil {
			panic(err)
		}
	},
	GroupID: "cfg",
}

var openCmd = &cobra.Command{
	Use:   "open [central public key]",
	Short: "Bundles provided bundle against the public key. Outputs the parsed configuration",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			_ = cmd.Usage()
			return
		}
		pkey := &state.NyPublicKey{}
		err := pkey.UnmarshalText([]byte(args[0]))
		if err != nil {
			panic(err)
		}

		inPath := cmd.Flag("input").Value.String()
		bundleStr, err := os.ReadFile(inPath)
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

		_, err = fmt.Fprint(os.Stderr, string(cfgYaml))
		if err != nil {
			return
		}
		println("Bundle is valid")
	},
	GroupID: "cfg",
}

func init() {
	rootCmd.AddCommand(sealCmd)

	sealCmd.Flags().StringP("config", "c", DefaultConfigPath, "central config path")
	sealCmd.Flags().StringP("key", "k", DefaultKeyPath, "central key path")
	sealCmd.Flags().StringP("output", "o", DefaultBundlePath, "bundle output path")

	rootCmd.AddCommand(openCmd)
	openCmd.Flags().StringP("input", "i", DefaultBundlePath, "Path to bundle input file")
}
