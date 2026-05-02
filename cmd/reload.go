package cmd

import (
	"fmt"
	"os"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/protocol"
	"github.com/spf13/cobra"
)

var reloadCmd = &cobra.Command{
	Use:     "reload",
	Short:   "Reload configuration without restart",
	GroupID: "ny",
	Run: func(cmd *cobra.Command, args []string) {
		itf, _ := cmd.Flags().GetString("interface")
		jsonOut, _ := cmd.Flags().GetBool("json")

		resp, err := core.SendIPCRequest(itf, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Reload{Reload: &protocol.ReloadRequest{}},
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
		r := resp.GetReload()
		fmt.Printf("Result: %s\n", r.Result.String())
		if r.Message != "" {
			fmt.Printf("Message: %s\n", r.Message)
		}
	},
}

func init() {
	rootCmd.AddCommand(reloadCmd)
	reloadCmd.Flags().StringP("interface", "i", "nylon", "Interface name")
	reloadCmd.Flags().Bool("json", false, "Output as JSON")
	reloadCmd.Flags().String("file", "", "Path to local central config file")
}
