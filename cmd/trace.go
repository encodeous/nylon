package cmd

import (
	"fmt"
	"os"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/protocol"
	"github.com/spf13/cobra"
)

var traceCmd = &cobra.Command{
	Use:     "trace",
	Short:   "Stream live packet-routing trace events",
	GroupID: "ny",
	Run: func(cmd *cobra.Command, args []string) {
		itf, _ := cmd.Flags().GetString("interface")
		first := true
		err := core.SendIPCStream(itf, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Trace{Trace: &protocol.TraceRequest{}},
		}, func(resp *protocol.IpcResponse) error {
			if first {
				first = false
				if !resp.Ok {
					fmt.Fprintln(os.Stderr, "Error:", resp.Error)
					os.Exit(1)
				}
				return nil
			}
			if t := resp.GetTrace(); t != nil {
				fmt.Print(t.Line)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(traceCmd)
	traceCmd.Flags().StringP("interface", "i", "nylon", "Interface name")
}
