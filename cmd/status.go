package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/moby/term"
	"github.com/spf13/cobra"
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

func renderStatus(s *protocol.StatusResponse, opts statusRenderOptions) {
	p := palette(opts.color)
	node := s.GetNode()
	stats := node.GetStats()

	fmt.Println(p.header("interface") + ": " + node.Interface)
	printKV(p, 1, "node", node.NodeId)
	printKV(p, 1, "public key", node.PublicKey)
	printKV(p, 1, "listening port", fmt.Sprint(node.ListenPort))
	printKV(p, 1, "config timestamp", fmt.Sprint(node.ConfigTimestamp))
	printKV(p, 1, "trace enabled", fmt.Sprint(node.TraceEnabled))
	printTable(p, 1,
		[]string{"neighbours", "active endpoints", "selected routes", "advertised", "tx", "rx"},
		[][]string{{
			fmt.Sprint(stats.NeighbourCount),
			fmt.Sprint(stats.ActiveEndpointCount),
			fmt.Sprint(stats.SelectedRouteCount),
			fmt.Sprint(stats.AdvertisedPrefixCount),
			formatBytes(stats.TxBytes),
			formatBytes(stats.RxBytes),
		}},
	)
	fmt.Println()

	if len(node.Seqnos) > 0 {
		fmt.Println(p.header("local seqnos"))
		printSeqnos(p, node.Seqnos)
		fmt.Println()
	}

	if len(node.Advertised) > 0 {
		fmt.Println(p.header("advertised"))
		printAdvertisements(p, node.Advertised, false)
		fmt.Println()
	}

	fmt.Println(p.header("peers"))
	if len(s.Neighbours) == 0 {
		fmt.Println("  " + p.muted("none"))
	}
	for _, neigh := range s.Neighbours {
		suffix := ""
		if neigh.PassiveClient {
			suffix = " " + p.warn("[passive client]")
		}
		fmt.Printf("  %s %s%s\n", p.key(neigh.PeerId), p.value("("+neigh.PublicKey+")"), suffix)
		best := bestEndpoint(neigh.Endpoints)
		bestMetric := uint32(state.INF)
		if best != nil {
			bestMetric = best.Metric
		}
		wg := neigh.GetWireguard()
		statHeaders := []string{"best metric", "latest handshake", "tx", "rx"}
		statRow := []string{metricText(p, bestMetric), formatHandshake(wg.LatestHandshakeUnix), formatBytes(wg.TxBytes), formatBytes(wg.RxBytes)}
		if wg.Endpoint != nil {
			statHeaders = append(statHeaders, "wireguard endpoint")
			statRow = append(statRow, *wg.Endpoint)
		}
		printTable(p, 2, statHeaders, [][]string{statRow})
		if len(neigh.Endpoints) > 0 {
			fmt.Println("    " + p.section("endpoints:"))
			printEndpoints(p, neigh.Endpoints, best, opts.showFull)
		}
		if opts.showRoutes && len(neigh.Routes) > 0 {
			fmt.Println("    " + p.section("advertised routes:"))
			printNeighRoutes(p, neigh.PeerId, neigh.Routes, opts.showFull)
		} else {
			fmt.Println("    " + p.section("advertised prefixes:"))
			printCondensedNeighRoutes(p, neigh.PeerId, neigh.Routes, 3)
		}
		if opts.showRoutes && len(neigh.Advertised) > 0 {
			fmt.Println("    " + p.section("local advertisements for peer:"))
			printAdvertisements(p, neigh.Advertised, true)
		}
		fmt.Println()
	}

	fmt.Println(p.header("routes"))
	printSelectedRoutes(p, s.GetRoutes().Selected, opts.showFull)
	if opts.showRoutes {
		printTableRoutes(p, "forward table", s.GetRoutes().Forward)
		printTableRoutes(p, "exit table", s.GetRoutes().Exit)
	}
	fmt.Println()

	if opts.showFull {
		fmt.Println(p.header("feasibility distances"))
		if len(s.FeasibilityDistances) == 0 {
			fmt.Println("  " + p.muted("none"))
		}
		printFeasibilityDistances(p, s.FeasibilityDistances)
	}
}

func printSelectedRoutes(p paletteValues, routes []*protocol.SelRoute, full bool) {
	fmt.Println("  " + p.key("selected routes"))
	if len(routes) == 0 {
		fmt.Println("    " + p.muted("none"))
		return
	}
	headers := []string{"prefix", "nh", "router", "seqno", "metric"}
	rows := make([][]string, 0, len(routes))
	for _, route := range routes {
		pub := route.GetPubRoute()
		src := pub.GetSource()
		fd := pub.GetFd()
		row := []string{src.Prefix, route.Nh, src.NodeId, fmt.Sprint(fd.Seqno), metricText(p, fd.Metric)}
		if full {
			if len(headers) == 5 {
				headers = append(headers, "expires", "retracted by")
			}
			retractedBy := ""
			if len(route.RetractedBy) > 0 {
				retractedBy = strings.Join(route.RetractedBy, ",")
			}
			row = append(row, formatExpiry(route.ExpireAtUnix), retractedBy)
		}
		rows = append(rows, row)
	}
	printTable(p, 2, headers, rows)
}

func printTableRoutes(p paletteValues, name string, routes []*protocol.RouteTableEntry) {
	fmt.Println("  " + p.key(name))
	if len(routes) == 0 {
		fmt.Println("    " + p.muted("none"))
		return
	}
	rows := make([][]string, 0, len(routes))
	for _, route := range routes {
		action := route.Nh
		if route.Blackhole {
			action = p.bad("blackhole")
		}
		rows = append(rows, []string{route.Prefix, action})
	}
	printTable(p, 2, []string{"prefix", "nh"}, rows)
}

func endpointFlags(p paletteValues, ep *protocol.EndpointInfo, best *protocol.EndpointInfo) string {
	flags := make([]string, 0)
	if ep.Active {
		flags = append(flags, p.good("active"))
	} else {
		flags = append(flags, p.warn("inactive"))
	}
	if best != nil && ep == best {
		flags = append(flags, p.good("best"))
	}
	if ep.RemoteInit {
		flags = append(flags, "remote")
	}
	if len(flags) == 0 {
		return ""
	}
	return "[" + strings.Join(flags, ",") + "]"
}

func bestEndpoint(endpoints []*protocol.EndpointInfo) *protocol.EndpointInfo {
	var best *protocol.EndpointInfo
	for _, ep := range endpoints {
		if !ep.Active {
			continue
		}
		if best == nil || ep.Metric < best.Metric || (ep.Metric == best.Metric && ep.Address < best.Address) {
			best = ep
		}
	}
	return best
}

func printEndpoints(p paletteValues, endpoints []*protocol.EndpointInfo, best *protocol.EndpointInfo, full bool) {
	headers := []string{"address", "resolved", "metric", "state"}
	if full {
		headers = append(headers, "rtt", "stable rtt")
	}
	rows := make([][]string, 0, len(endpoints))
	for _, ep := range endpoints {
		resolved := p.warn("unresolved")
		if ep.Resolved != nil {
			resolved = *ep.Resolved
		}
		row := []string{ep.Address, resolved, metricText(p, ep.Metric), endpointFlags(p, ep, best)}
		if full {
			row = append(row, formatDurationNs(ep.FilteredRttNs), formatDurationNs(ep.StabilizedRttNs))
		}
		rows = append(rows, row)
	}
	printTable(p, 3, headers, rows)
}

func printNeighRoutes(p paletteValues, neigh string, routes []*protocol.NeighRoute, full bool) {
	headers := []string{"prefix", "router", "seqno", "metric"}
	if full {
		headers = append(headers, "expires")
	}
	rows := make([][]string, 0, len(routes))
	for _, route := range routes {
		pub := route.GetPubRoute()
		src := pub.GetSource()
		fd := pub.GetFd()
		prefix := src.Prefix
		if src.NodeId == neigh {
			prefix = p.good(prefix)
		}
		row := []string{prefix, src.NodeId, fmt.Sprint(fd.Seqno), metricText(p, fd.Metric)}
		if full {
			row = append(row, formatExpiry(route.ExpireAtUnix))
		}
		rows = append(rows, row)
	}
	printTable(p, 3, headers, rows)
}

func printCondensedNeighRoutes(p paletteValues, neigh string, routes []*protocol.NeighRoute, indent int) {
	prefixes := make([]string, 0, len(routes))
	for _, route := range routes {
		if route.PubRoute.Source.NodeId != neigh {
			continue
		}
		prefixes = append(prefixes, route.PubRoute.GetSource().Prefix)
	}
	printCommaList(p, indent, prefixes)
}

func printAdvertisements(p paletteValues, advertisements []*protocol.Advertisement, nested bool) {
	indent := 1
	if nested {
		indent = 3
	}
	rows := make([][]string, 0, len(advertisements))
	for _, adv := range advertisements {
		hold := ""
		if adv.PassiveHold {
			hold = p.warn("passive-hold")
		}
		rows = append(rows, []string{adv.Prefix, adv.NodeId, metricText(p, adv.Metric), formatExpiry(adv.ExpiryUnix), hold})
	}
	printTable(p, indent, []string{"prefix", "router", "metric", "expires", "state"}, rows)
}

func printSeqnos(p paletteValues, seqnos []*protocol.SeqnoEntry) {
	rows := make([][]string, 0, len(seqnos))
	for _, seq := range seqnos {
		rows = append(rows, []string{seq.Prefix, fmt.Sprint(seq.Seqno)})
	}
	printTable(p, 1, []string{"prefix", "seqno"}, rows)
}

func printFeasibilityDistances(p paletteValues, distances []*protocol.FeasibilityDistance) {
	rows := make([][]string, 0, len(distances))
	for _, dist := range distances {
		src := dist.GetSource()
		fd := dist.GetFd()
		rows = append(rows, []string{src.Prefix, src.NodeId, fmt.Sprint(fd.Seqno), metricText(p, fd.Metric)})
	}
	printTable(p, 1, []string{"prefix", "router", "seqno", "metric"}, rows)
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().StringP("interface", "i", "nylon", "Interface name")
	statusCmd.Flags().Bool("json", false, "Output as JSON")
	statusCmd.Flags().Bool("routes", false, "Show route tables and neighbour route advertisements")
	statusCmd.Flags().Bool("full", false, "Show full routing internals")
	statusCmd.Flags().Bool("no-color", false, "Disable colored output")
}
