package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"os"
	"slices"
	"strings"
)

var clientCmd = &cobra.Command{
	Use:   "new-client",
	Short: "Create a new passive WireGuard client",
	Run: func(cmd *cobra.Command, args []string) {
		var centralCfg state.CentralCfg
		file, err := os.ReadFile(state.CentralConfigPath)
		if err != nil {
			panic(err)
		}
		err = yaml.Unmarshal(file, &centralCfg)
		if err != nil {
			panic(err)
		}

		err = state.CentralConfigValidator(&centralCfg)
		if err != nil {
			panic(err)
		}

		pkey := state.GenerateKey()

		println("Please select a gateway router for this client to connect to:")
		router := promptSelectRouter(centralCfg)
		rIdx := slices.IndexFunc(centralCfg.Routers, func(cfg state.RouterCfg) bool {
			return cfg.Id == router
		})
		rCfg := centralCfg.Routers[rIdx]

		sb := strings.Builder{}
		sb.WriteString("[Interface]\n")
		out, _ := pkey.MarshalText()
		sb.WriteString(fmt.Sprintf("PrivateKey = %s\n", string(out)))
		sb.WriteString("Address = <your prefix>\n\n")
		sb.WriteString("[Peer]\n")
		sb.WriteString("PersistentKeepalive = 25\n")
		routerPkey, err := rCfg.PubKey.MarshalText()
		if err != nil {
			panic(err)
		}
		sb.WriteString(fmt.Sprintf("PublicKey = %s\n", string(routerPkey)))
		if len(rCfg.Endpoints) != 0 {
			sb.WriteString(fmt.Sprintf("Endpoint = %s\n", rCfg.Endpoints[0].String()))
		} else {
			sb.WriteString("Endpoint = <specify endpoint>\n")
		}
		allowedIps := make([]string, 0)
		for _, node := range centralCfg.GetNodes() {
			for _, prefix := range node.Prefixes {
				allowedIps = append(allowedIps, prefix.String())
			}
		}
		sb.WriteString(fmt.Sprintf("AllowedIPs = %s\n\n", strings.Join(allowedIps, ", ")))

		fmt.Println("WireGuard Client Configuration:")
		fmt.Println(sb.String())
		fmt.Println("Please add this client's public key to the central config.")
		out, _ = pkey.Pubkey().MarshalText()
		fmt.Printf(`clients:
  - id: your-client
    pubkey: %s
    prefixes:
      - <your prefix>

`, string(out))
	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(clientCmd)
}
