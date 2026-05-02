package cmd

import (
	"fmt"
	"os"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/protocol"
	"github.com/spf13/cobra"
)

var probeCmd = &cobra.Command{
	Use:     "probe <peer-node-id>",
	Short:   "Probe all endpoints of a neighbour",
	Args:    cobra.ExactArgs(1),
	GroupID: "ny",
	Run: func(cmd *cobra.Command, args []string) {
		itf, _ := cmd.Flags().GetString("interface")
		jsonOut, _ := cmd.Flags().GetBool("json")
		resp, err := core.SendIPCRequest(itf, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: args[0]}},
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
		if !resp.Ok {
			fmt.Fprintln(os.Stderr, "Error:", resp.Error)
			os.Exit(1)
		}
		if jsonOut {
			printJSON(resp)
			return
		}
		for _, r := range resp.GetProbe().Results {
			status := "ok"
			if !r.Success {
				status = "error: " + r.Error
			}
			fmt.Printf("  %s  %s\n", r.Address, status)
		}
	},
}

func init() {
	rootCmd.AddCommand(probeCmd)
	probeCmd.Flags().StringP("interface", "i", "nylon", "Interface name")
	probeCmd.Flags().Bool("json", false, "Output as JSON")
}
