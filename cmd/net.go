package cmd

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"gopkg.in/yaml.v3"
	"os"
)

// netCmd represents the net command
var netCmd = &cobra.Command{
	Use:   "new-net",
	Short: "Create a new nylon network with central configuration",
	Run: func(cmd *cobra.Command, args []string) {

		dpKey, err := wgtypes.GeneratePrivateKey()
		if err != nil {
			panic(err)
		}
		ecKey, err := ecdh.X25519().NewPrivateKey(dpKey[:])
		if err != nil {
			panic(err)
		}
		_, ctlKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}
		nodeCfg := state.NodeCfg{
			Key:   state.EdPrivateKey(ctlKey),
			WgKey: (*state.EcPrivateKey)(ecKey),
			Id:    "my-node",
		}

		if res, _ := cmd.Flags().GetBool("skip"); res {
			goto SkipInteract1
		}
		fmt.Println("Nylon Initialization Wizard")

		fmt.Println("Node Configuration")
		fmt.Println("Give this node a name:")
		nodeCfg.Id = state.Node(promptDefaultStr("name", string(nodeCfg.Id), state.NameValidator))

		fmt.Println("Where should the control-plane listen to?:")
		nodeCfg.CtlBind = promptDefaultAddrPort("[TCP] ip:port", "0.0.0.0:54003", state.BindValidator)
		fmt.Println("Where should the data-plane (WireGuard) listen to?:")
		nodeCfg.DpBind = promptDefaultAddrPort("[UDP] ip:port", "0.0.0.0:54004", state.BindValidator)
		fmt.Println("Where should the data-plane probe (for discovery & metric) listen to?:")
		nodeCfg.ProbeBind = promptDefaultAddrPort("[UDP] ip:port", "0.0.0.0:54003", state.BindValidator)

		fmt.Println("\nNOTE: You should make these ports accessible for best reliability and performance.\nIf it is not possible, as long as one node in the network is reachable, nylon can still work!\n\n")

		nodeConfigPath = safeSaveFile(nodeConfigPath, "Node Config")

	SkipInteract1:
		ncfg, err := yaml.Marshal(&nodeCfg)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(nodeConfigPath, ncfg, 0700)
		if err != nil {
			panic(err)
		}

		rootPubkey, rootPrivkey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			panic(err)
		}

		centralConfig := state.CentralCfg{
			RootPubKey: state.EdPrivateKey(rootPubkey),
			Nodes: []state.PubNodeCfg{
				nodeCfg.GeneratePubCfg(),
			},
			Edges: []state.Pair[state.Node, state.Node]{
				{nodeCfg.Id, "other-node"},
			},
			Version: 0,
		}

		if res, _ := cmd.Flags().GetBool("skip"); res {
			goto SkipInteract2
		}

		fmt.Println("\n\nCentral Network Configuration")

		centralConfigPath = safeSaveFile(centralConfigPath, "Central Config")
		centralKeyPath = safeSaveFile(centralKeyPath, "Central Key")

	SkipInteract2:
		ccfg, err := yaml.Marshal(&centralConfig)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(centralConfigPath, ccfg, 0700)
		if err != nil {
			panic(err)
		}

		key, err := state.EdPrivateKey(rootPrivkey).MarshalText()
		if err != nil {
			panic(err)
		}
		err = os.WriteFile(centralKeyPath, key, 0700)
		if err != nil {
			panic(err)
		}

	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(netCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// netCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// netCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	netCmd.Flags().BoolP("skip", "s", false, "Skip the wizard and create the config file directly")
}
