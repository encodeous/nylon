package cmd

import (
	"fmt"
	"os"

	"github.com/encodeous/nylon/ipc"
	"github.com/encodeous/nylon/protocol"
	"github.com/spf13/cobra"
)

var traceCmd = &cobra.Command{
	Use:     "trace",
	Short:   "Stream live packet-routing trace events",
	GroupID: "ny",
	Run: func(cmd *cobra.Command, args []string) {
		itf, _ := cmd.Flags().GetString("interface")
		client, err := ipc.Dial(itf)
		if err == nil {
			defer client.Close()
			err = client.Stream(&protocol.IpcRequest{
				Request: &protocol.IpcRequest_Trace{Trace: &protocol.TraceRequest{}},
			}, func(resp *protocol.IpcResponse) bool {
				fmt.Print(resp.GetTrace().GetLine())
				return true
			})
		}
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
