package cmd

import (
	"bufio"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
)

var genKeyCmd = &cobra.Command{
	Use:   "genkey",
	Short: "Generates a new Nylon private key.",
	Run: func(cmd *cobra.Command, args []string) {
		privKey := state.GenerateKey()
		privKeyStr, err := privKey.MarshalText()
		if err != nil {
			panic(err)
		}
		fmt.Println(string(privKeyStr))
	},
	GroupID: "init",
}

var pubKeyCmd = &cobra.Command{
	Use:   "pubkey",
	Short: "Derives the public key from a provided private key.",
	Run: func(cmd *cobra.Command, args []string) {
		in := bufio.NewReader(os.Stdin)
		ln, err := in.ReadString('\n')
		if err != nil {
			panic(err)
		}
		privKey := state.NyPrivateKey{}
		err = privKey.UnmarshalText([]byte(ln))
		if err != nil {
			panic(err)
		}
		pubKeyStr, err := privKey.Pubkey().MarshalText()
		if err != nil {
			panic(err)
		}
		fmt.Println(string(pubKeyStr))
	},
	GroupID: "init",
}

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Generates a new Nylon Keypair. Outputs Private Key to stdout, Public Key to Stderr.",
	Run: func(cmd *cobra.Command, args []string) {
		privKey := state.GenerateKey()
		privKeyStr, err := privKey.MarshalText()
		if err != nil {
			panic(err)
		}
		fmt.Println(string(privKeyStr))
		pubKeyStr, err := privKey.Pubkey().MarshalText()
		_, err = fmt.Fprintln(os.Stderr, string(pubKeyStr))
		if err != nil {
			panic(err)
		}
	},
	GroupID: "init",
}

var versionCmd = &cobra.Command{
	Use:     "version",
	Aliases: []string{"v"},
	Short:   "Gets the nylon release version",
	Run: func(cmd *cobra.Command, args []string) {
		val, ok := debug.ReadBuildInfo()
		if !ok {
			fmt.Println("unable to find version")
		}
		fmt.Printf("Version: %s\n", val.Main.Version)
	},
	GroupID: "ny",
}

func init() {
	// wireguard-style key generation
	rootCmd.AddCommand(genKeyCmd)
	rootCmd.AddCommand(pubKeyCmd)

	// combined key generation and pubkey derivation
	rootCmd.AddCommand(keyCmd)

	rootCmd.AddCommand(versionCmd)
}
