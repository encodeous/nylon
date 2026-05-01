package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/moby/term"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
)

var statusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show node status",
	GroupID: "ny",
	Run: func(cmd *cobra.Command, args []string) {
		itf, _ := cmd.Flags().GetString("interface")
		jsonOut, _ := cmd.Flags().GetBool("json")
		showRoutes, _ := cmd.Flags().GetBool("routes")
		showFull, _ := cmd.Flags().GetBool("full")
		noColor, _ := cmd.Flags().GetBool("no-color")

		resp, err := core.SendIPCRequest(itf, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Status{Status: &protocol.StatusRequest{}},
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
		renderStatus(resp.GetStatus(), statusRenderOptions{
			showRoutes: showRoutes || showFull,
			showFull:   showFull,
			color:      !noColor && os.Getenv("NO_COLOR") == "" && term.IsTerminal(os.Stdout.Fd()),
		})
	},
}

type statusRenderOptions struct {
	showRoutes bool
	showFull   bool
	color      bool
}

type statusPalette struct {
	header func(string) string
	key    func(string) string
	good   func(string) string
	warn   func(string) string
	bad    func(string) string
	muted  func(string) string
}

func palette(enabled bool) statusPalette {
	paint := func(code string) func(string) string {
		if !enabled {
			return func(s string) string { return s }
		}
		return func(s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }
	}
	return statusPalette{
		header: paint("1;36"),
		key:    paint("1"),
		good:   paint("32"),
		warn:   paint("33"),
		bad:    paint("31"),
		muted:  paint("2"),
	}
}

func renderStatus(s *protocol.StatusResponse, opts statusRenderOptions) {
	p := palette(opts.color)
	node := s.GetNode()
	stats := node.GetStats()

	fmt.Println(p.header("interface") + ": " + node.Interface)
	fmt.Printf("  %s: %s\n", p.key("node"), node.NodeId)
	fmt.Printf("  %s: %s\n", p.key("public key"), node.PublicKey)
	fmt.Printf("  %s: %d\n", p.key("listening port"), node.ListenPort)
	fmt.Printf("  %s: %d\n", p.key("config timestamp"), node.ConfigTimestamp)
	fmt.Printf("  %s: %v\n", p.key("trace enabled"), node.TraceEnabled)
	fmt.Printf("  %s: neighbours=%d active_endpoints=%d selected_routes=%d advertised=%d tx=%s rx=%s\n",
		p.key("stats"), stats.NeighbourCount, stats.ActiveEndpointCount, stats.SelectedRouteCount,
		stats.AdvertisedPrefixCount, formatBytes(stats.TxBytes), formatBytes(stats.RxBytes))

	if len(node.Advertised) > 0 {
		fmt.Println()
		fmt.Println(p.header("advertised"))
		for _, adv := range node.Advertised {
			fmt.Printf("  %s from %s metric %d expires %s%s\n",
				adv.Prefix, adv.NodeId, adv.Metric, formatExpiry(adv.ExpiryUnix), passiveHoldSuffix(p, adv.PassiveHold))
		}
	}

	fmt.Println()
	fmt.Println(p.header("peers"))
	if len(s.Neighbours) == 0 {
		fmt.Println("  " + p.muted("none"))
	}
	for _, neigh := range s.Neighbours {
		kind := "router"
		if neigh.PassiveClient {
			kind = "passive-client"
		}
		fmt.Printf("  %s (%s)\n", p.key(neigh.PeerId), kind)
		fmt.Printf("    public key: %s\n", neigh.PublicKey)
		fmt.Printf("    best metric: %s\n", metricText(p, neigh.BestMetric))
		wg := neigh.GetWireguard()
		fmt.Printf("    latest handshake: %s; transfer: %s received, %s sent\n",
			formatHandshake(wg.LatestHandshakeUnixNano), formatBytes(wg.RxBytes), formatBytes(wg.TxBytes))
		if wg.Endpoint != "" {
			fmt.Printf("    wireguard endpoint: %s\n", wg.Endpoint)
		}
		if len(neigh.Endpoints) > 0 {
			fmt.Println("    endpoints:")
			for _, ep := range neigh.Endpoints {
				flags := endpointFlags(p, ep)
				resolved := ep.Resolved
				if resolved == "" {
					resolved = p.warn("unresolved")
				}
				fmt.Printf("      - %s resolved %s metric %s%s\n", ep.Address, resolved, metricText(p, ep.Metric), flags)
			}
		}
		if opts.showRoutes && len(neigh.Routes) > 0 {
			fmt.Println("    advertised routes:")
			for _, route := range neigh.Routes {
				printNeighRoute(p, route, opts.showFull)
			}
		}
		if opts.showRoutes && len(neigh.Advertised) > 0 {
			fmt.Println("    local advertisements for peer:")
			for _, adv := range neigh.Advertised {
				fmt.Printf("      - %s metric %d expires %s%s\n", adv.Prefix, adv.Metric, formatExpiry(adv.ExpiryUnix), passiveHoldSuffix(p, adv.PassiveHold))
			}
		}
	}

	if opts.showRoutes {
		fmt.Println()
		fmt.Println(p.header("routes"))
		printSelectedRoutes(p, s.GetRoutes().Selected, opts.showFull)
		printTableRoutes(p, "forward", s.GetRoutes().Forward)
		printTableRoutes(p, "exit", s.GetRoutes().Exit)
	}

	if opts.showFull {
		fmt.Println()
		fmt.Println(p.header("local seqnos"))
		if len(node.Seqnos) == 0 {
			fmt.Println("  " + p.muted("none"))
		}
		for _, seq := range node.Seqnos {
			fmt.Printf("  %s seqno %d\n", seq.Prefix, seq.Seqno)
		}

		fmt.Println()
		fmt.Println(p.header("feasibility distances"))
		if len(s.FeasibilityDistances) == 0 {
			fmt.Println("  " + p.muted("none"))
		}
		for _, fd := range s.FeasibilityDistances {
			src := fd.GetSource()
			val := fd.GetFd()
			fmt.Printf("  router %s prefix %s seqno %d metric %s\n", src.NodeId, src.Prefix, val.Seqno, metricText(p, val.Metric))
		}
	}
}

func printSelectedRoutes(p statusPalette, routes []*protocol.SelRoute, full bool) {
	fmt.Println("  " + p.key("selected"))
	if len(routes) == 0 {
		fmt.Println("    " + p.muted("none"))
	}
	for _, route := range routes {
		pub := route.GetPubRoute()
		src := pub.GetSource()
		fd := pub.GetFd()
		extra := ""
		if full {
			extra = fmt.Sprintf(" expires %s", formatExpiry(route.ExpireAtUnix))
			if len(route.RetractedBy) > 0 {
				extra += " retracted_by=" + strings.Join(route.RetractedBy, ",")
			}
		}
		fmt.Printf("    %s via %s source %s seqno %d metric %s%s\n", src.Prefix, route.Nh, src.NodeId, fd.Seqno, metricText(p, fd.Metric), extra)
	}
}

func printTableRoutes(p statusPalette, name string, routes []*protocol.RouteTableEntry) {
	fmt.Println("  " + p.key(name))
	if len(routes) == 0 {
		fmt.Println("    " + p.muted("none"))
	}
	for _, route := range routes {
		if route.Blackhole {
			fmt.Printf("    %s %s\n", route.Prefix, p.bad("blackhole"))
		} else {
			fmt.Printf("    %s via %s\n", route.Prefix, route.Nh)
		}
	}
}

func printNeighRoute(p statusPalette, route *protocol.NeighRoute, full bool) {
	pub := route.GetPubRoute()
	src := pub.GetSource()
	fd := pub.GetFd()
	extra := ""
	if full {
		extra = fmt.Sprintf(" expires %s", formatExpiry(route.ExpireAtUnix))
	}
	fmt.Printf("      - %s source %s seqno %d metric %s%s\n", src.Prefix, src.NodeId, fd.Seqno, metricText(p, fd.Metric), extra)
}

func endpointFlags(p statusPalette, ep *protocol.EndpointInfo) string {
	flags := make([]string, 0)
	if ep.Active {
		flags = append(flags, p.good("active"))
	} else {
		flags = append(flags, p.warn("inactive"))
	}
	if ep.IsBest {
		flags = append(flags, p.good("best"))
	}
	if ep.RemoteInit {
		flags = append(flags, "remote")
	}
	if len(flags) == 0 {
		return ""
	}
	return " [" + strings.Join(flags, ",") + "]"
}

func passiveHoldSuffix(p statusPalette, passiveHold bool) string {
	if !passiveHold {
		return ""
	}
	return " " + p.warn("[passive-hold]")
}

func metricText(p statusPalette, metric uint32) string {
	if metric >= state.INFM {
		return p.bad("INF")
	}
	return fmt.Sprintf("%d", metric)
}

func formatExpiry(unix int64) string {
	if unix <= 0 {
		return "never"
	}
	expiry := time.Unix(unix, 0)
	rem := time.Until(expiry)
	if rem > 24*time.Hour || expiry.Year() > time.Now().Year()+10 {
		return "never"
	}
	if rem < 0 {
		return "expired"
	}
	return rem.Truncate(time.Second).String()
}

func formatHandshake(unixNano int64) string {
	if unixNano == 0 {
		return "never"
	}
	return time.Since(time.Unix(0, unixNano)).Truncate(time.Second).String() + " ago"
}

func formatBytes(v uint64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := uint64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(v)/float64(div), "KMGTPE"[exp])
}

func printJSON(resp *protocol.IpcResponse) {
	m := protojson.MarshalOptions{Indent: "  ", EmitUnpopulated: true}
	data, _ := m.Marshal(resp)
	fmt.Println(string(data))
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().StringP("interface", "i", "nylon", "Interface name")
	statusCmd.Flags().Bool("json", false, "Output as JSON")
	statusCmd.Flags().Bool("routes", false, "Show route tables and neighbour route advertisements")
	statusCmd.Flags().Bool("full", false, "Show full routing internals")
	statusCmd.Flags().Bool("no-color", false, "Disable colored output")
}
