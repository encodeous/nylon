package cmd

import (
	"bufio"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"maps"
	"os"
	"slices"
	"strings"
)

var genKey = false

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Generates a new Nylon Keypair. Outputs Private Key to stdout, Public Key to Stderr.",
	Run: func(cmd *cobra.Command, args []string) {
		privKey := state.NyPrivateKey{}
		if !genKey {
			in := bufio.NewReader(os.Stdin)
			ln, err := in.ReadString('\n')
			if err != nil {
				panic(err)
			}

			err = privKey.UnmarshalText([]byte(ln))
			if err != nil {
				return
			}
		} else {
			privKey = state.GenerateKey()
			privKeyStr, err := privKey.MarshalText()
			if err != nil {
				panic(err)
			}
			fmt.Println(string(privKeyStr))
		}

		pubKeyStr, err := privKey.Pubkey().MarshalText()
		_, err = fmt.Fprintln(os.Stderr, string(pubKeyStr))
		if err != nil {
			panic(err)
		}
	},
	GroupID: "init",
}

var hostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "Generates a static hosts override for hosts on the network",
	Run: func(cmd *cobra.Command, args []string) {
		cfgFile, err := os.ReadFile(cmd.Flag("config").Value.String())
		if err != nil {
			panic(err)
		}
		cfg := state.CentralCfg{}
		err = yaml.Unmarshal(cfgFile, &cfg)
		if err != nil {
			panic(err)
		}
		hosts := make(map[string][]string)
		for _, node := range cfg.GetNodes() {
			primaryIp := node.Prefixes[0].Addr().String()
			hosts[primaryIp] = append(hosts[primaryIp], string(node.Id))
		}
		for domain, node := range cfg.Hosts {
			primaryIp := cfg.GetNode(state.NodeId(node)).Prefixes[0].Addr().String()
			hosts[primaryIp] = append(hosts[primaryIp], domain)
		}
		sb := strings.Builder{}
		for _, ip := range slices.Sorted(maps.Keys(hosts)) {
			sb.WriteString(ip)
			domains := hosts[ip]
			for _, domain := range slices.Sorted(slices.Values(domains)) {
				sb.WriteString(fmt.Sprintf("\t%s", domain))
			}
			sb.WriteString("\n")
		}
		fmt.Print(sb.String())
	},
	GroupID: "cfg",
}

func init() {
	rootCmd.AddCommand(hostsCmd)
	hostsCmd.Flags().StringP("config", "c", DefaultConfigPath, "Path to the config file")

	rootCmd.AddCommand(keyCmd)
	keyCmd.Flags().BoolVarP(&genKey, "gen", "g", true, "generate a new keypair")
}
