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
		pubKey, err := key.Pubkey().MarshalText()
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
}
