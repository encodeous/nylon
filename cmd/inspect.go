package cmd

import (
	"fmt"

	"github.com/encodeous/nylon/core"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:     "inspect",
	Aliases: []string{"i"},
	Short:   "Inspects the current state of nylon",
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			fmt.Println("Usage: nylon inspect <interface>")
			return
		}
		itf := args[0]
		result, err := core.IPCGet(itf)
		if err != nil {
			fmt.Println("Error:", err.Error())
			return
		}
		fmt.Print(result)
	},
	GroupID: "ny",
}

func init() {
	rootCmd.AddCommand(inspectCmd)
}
