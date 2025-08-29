package cmd

import (
	"github.com/encodeous/nylon/core"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:     "inspect",
	Aliases: []string{"i"},
	Short:   "Inspects the current state of nylon",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			println("Usage: nylon inspect <interface>")
			return
		}
		itf := args[0]
		result, err := core.IPCGet(itf)
		if err != nil {
			println("Error:", err.Error())
			return
		}
		println(result)
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}
