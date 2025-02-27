package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"net/netip"
	"os"
	"time"
)

// netCmd represents the net command
var netCmd = &cobra.Command{
	Use:   "new-net",
	Short: "Create a new nylon network with central configuration",
	Run: func(cmd *cobra.Command, args []string) {
		nodeCfg := promptCreateNode()

		ncfg, err := yaml.Marshal(&nodeCfg)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(state.NodeConfigPath, ncfg, 0700)
		if err != nil {
			panic(err)
		}

		pkey := state.GenerateKey()

		centralConfig := state.CentralCfg{
			RootKey: pkey.XPubkey(),
			Routers: []state.RouterCfg{
				{
					NodeCfg: state.NodeCfg{
						Id: "sample_node1",
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.0.1/32"),
							netip.MustParsePrefix("10.0.0.2/32"),
							netip.MustParsePrefix("10.1.0.0/16"),
						},
						PubKey: state.NyPublicKey{},
					},
					Endpoints: []netip.AddrPort{
						netip.MustParseAddrPort(fmt.Sprintf("8.8.8.8:%d", nodeCfg.Port)),
					},
				},
			},
			Clients: []state.ClientCfg{
				{
					NodeCfg: state.NodeCfg{
						Id:     "external-client",
						PubKey: state.NyPublicKey{},
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.2.0.1/32"),
						},
					},
				},
			},
			Graph: []string{
				"Group1 = node1, node2",
				"Group2 = node5, node6",
				"Group1, Group2, node7",
			},
			Timestamp: time.Now().UnixNano(),
		}

		fmt.Println("Where should the central config be saved?:")
		state.CentralConfigPath = safeSaveFile(state.CentralConfigPath, "Central Config")
		fmt.Println("Where should the central key be saved?:")
		state.CentralKeyPath = safeSaveFile(state.CentralKeyPath, "Central Key")
		ccfg, err := yaml.Marshal(&centralConfig)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(state.CentralConfigPath, ccfg, 0700)
		if err != nil {
			panic(err)
		}

		key, err := pkey.MarshalText()
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(state.CentralKeyPath, key, 0700)
		if err != nil {
			panic(err)
		}

	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(netCmd)
}
