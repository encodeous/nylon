package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"maps"
	"os"
	"slices"
	"strings"
)

var hostsCmd = &cobra.Command{
	Use:   "hosts",
	Short: "Generates a static hosts override for hosts on the network",
	Run: func(cmd *cobra.Command, args []string) {
		cfgFile, err := os.ReadFile(state.CentralConfigPath)
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
		fmt.Println(sb.String())
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(hostsCmd)
}
