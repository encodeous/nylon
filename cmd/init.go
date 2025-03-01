package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
)

var newCmd = &cobra.Command{
	Use:   "new [name]",
	Short: "Create a node configuration",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			_ = cmd.Usage()
			return
		}
		port, _ := strconv.Atoi(cmd.Flag("port").Value.String())

		name := args[0]
		err := state.NameValidator(name)
		if err != nil {
			fmt.Printf("Invalid name: %s\n", name)
			os.Exit(-1)
		}

		nodeCfg := state.LocalCfg{
			Key:  state.GenerateKey(),
			Id:   state.NodeId(name),
			Port: uint16(port),
		}

		ncfg, err := yaml.Marshal(&nodeCfg)
		if err != nil {
			panic(err)
		}

		outPath := cmd.Flag("output").Value.String()
		err = os.WriteFile(outPath, ncfg, 0700)
		if err != nil {
			panic(err)
		}
	},
	GroupID: "init",
}

var clientCmd = &cobra.Command{
	Use:   "client [gateway-router id]",
	Short: "Create a new passive WireGuard client",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			_ = cmd.Usage()
			return
		}
		router := state.NodeId(args[0])

		var centralCfg state.CentralCfg
		inPath := cmd.Flag("input").Value.String()
		file, err := os.ReadFile(inPath)
		if err != nil {
			panic(err)
		}
		err = yaml.Unmarshal(file, &centralCfg)
		if err != nil {
			panic(err)
		}

		err = state.CentralConfigValidator(&centralCfg)
		if err != nil {
			slog.Warn("Central Config is not valid!", "err", err)
		}

		pkey := state.GenerateKey()

		rIdx := slices.IndexFunc(centralCfg.Routers, func(cfg state.RouterCfg) bool {
			return cfg.Id == router
		})

		if rIdx == -1 {
			slog.Error("Router not found for this client", "router", router)
			os.Exit(-1)
		}

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

		fmt.Println(sb.String())

		fmt.Println("---")

		out, _ = pkey.Pubkey().MarshalText()
		_, err = fmt.Fprint(os.Stderr, string(out))
		if err != nil {
			return
		}
	},
	GroupID: "init",
}

func init() {
	rootCmd.AddCommand(newCmd)
	newCmd.Flags().StringP("output", "o", DefaultNodeConfigPath, "node config output file path")
	newCmd.Flags().Uint16P("port", "p", 57175, "UDP port to use")

	rootCmd.AddCommand(clientCmd)
	clientCmd.Flags().StringP("config", "c", DefaultConfigPath, "Path to the config file")
}
