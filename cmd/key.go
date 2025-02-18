package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"os"
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Generates a new Nylon Keypair",
	Run: func(cmd *cobra.Command, args []string) {
		key := state.GenerateKey()
		privKey, err := key.MarshalText()
		pubKey, err := key.XPubkey().MarshalText()
		if err != nil {
			panic(err)
		}
		fmt.Printf("PrivateKey=%s\n", privKey)
		_, err = fmt.Fprintf(os.Stderr, "PublicKey=%s\n", pubKey)
		if err != nil {
			panic(err)
		}
	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(keyCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// netCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// netCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
