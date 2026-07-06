package cmd

import (
	"fmt"
	"os"
	"time"

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
		timeout, _ := cmd.Flags().GetDuration("timeout")
		if timeout <= 0 {
			fmt.Fprintln(os.Stderr, "Error: timeout must be positive")
			os.Exit(1)
		}
		resp, err := core.SendIPCRequest(itf, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{
				PeerId:    args[0],
				TimeoutMs: durationMillis(timeout),
			}},
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
			fmt.Printf("  %s  %s", r.Address, endpointProbeStatus(r))
			if r.Resolved != nil && *r.Resolved != "" && *r.Resolved != r.Address {
				fmt.Printf("  resolved=%s", *r.Resolved)
			}
			if r.Status == protocol.EndpointProbeStatus_ENDPOINT_PROBE_REPLIED {
				fmt.Printf("  latency=%s", formatDurationNs(r.LatencyNs))
			}
			fmt.Println()
		}
	},
}

func init() {
	rootCmd.AddCommand(probeCmd)
	probeCmd.Flags().StringP("interface", "i", "nylon", "Interface name")
	probeCmd.Flags().Bool("json", false, "Output as JSON")
	probeCmd.Flags().Duration("timeout", 2*time.Second, "Probe response timeout")
}

func durationMillis(d time.Duration) uint32 {
	ms := d.Milliseconds()
	if ms <= 0 {
		return 1
	}
	if ms > int64(^uint32(0)) {
		return ^uint32(0)
	}
	return uint32(ms)
}

func endpointProbeStatus(r *protocol.EndpointProbeResult) string {
	switch r.Status {
	case protocol.EndpointProbeStatus_ENDPOINT_PROBE_REPLIED:
		return "ok"
	case protocol.EndpointProbeStatus_ENDPOINT_PROBE_TIMEOUT:
		return "timeout"
	case protocol.EndpointProbeStatus_ENDPOINT_PROBE_SEND_ERROR:
		return "send-error"
	case protocol.EndpointProbeStatus_ENDPOINT_PROBE_RESOLVE_ERROR:
		return "resolve-error"
	default:
		return "unknown"
	}
}
