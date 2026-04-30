package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/state"
)

func IPCGet(itf string) (string, error) {
	conn, err := ipc.UAPIDial(itf)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	_, err = rw.WriteString("get=nylon\n")
	if err != nil {
		return "", err
	}

	_, err = rw.WriteString("inspect\n")
	if err != nil {
		return "", err
	}
	err = rw.Flush()
	if err != nil {
		return "", err
	}

	res, err := rw.ReadString(0)
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSuffix(res, "\x00"), nil
}

func IPCTrace(itf string) error {
	conn, err := ipc.UAPIDial(itf)
	if err != nil {
		return err
	}
	defer conn.Close()
	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	_, err = rw.WriteString("get=nylon\n")
	if err != nil {
		return err
	}

	_, err = rw.WriteString("trace\n")
	if err != nil {
		return err
	}
	err = rw.Flush()
	if err != nil {
		return err
	}

	for {
		str, err := rw.ReadString('\n')
		if err != nil {
			return err
		}
		fmt.Print(str)
	}
}

func HandleNylonIPCGet(n *Nylon, rw *bufio.ReadWriter) error {
	cmd, err := rw.ReadString('\n')
	if err != nil {
		return err
	}
	sb := strings.Builder{}
	switch cmd {
	case "inspect\n":
		// print neighbours
		sb.WriteString("Neighbours:\n")
		for _, node := range n.RouterState.Neighbours {
			fmt.Fprintf(&sb, " - %s\n", node.Id)
			met := state.INF
			if node.BestEndpoint() != nil {
				met = node.BestEndpoint().Metric()
			}
			fmt.Fprintf(&sb, "   Metric: %d\n", met)
			fmt.Fprintf(&sb, "   Endpoints:\n")
			for _, ep := range node.Eps {
				nep := ep.AsNylonEndpoint()
				ap, err := nep.DynEP.Get()
				if err != nil {
					fmt.Fprintf(&sb, "    - %s (unresolved)\n", nep.DynEP.Value)
				} else {
					sb.WriteString(fmt.Sprintf("    - %s (resolved: %s) active=%v metric=%d\n", nep.DynEP.Value, ap.String(), nep.IsActive(), nep.Metric()))
				}
			}
			fmt.Fprintf(&sb, "   Published Routes:\n")
			rt := make([]string, 0)
			if len(node.Routes) == 0 {
				rt = append(rt, "    (none)")
			}
			for _, r := range node.Routes {
				rt = append(rt, fmt.Sprintf("    - %s", r.String()))
			}
			slices.Sort(rt)
			sb.WriteString(strings.Join(rt, "\n") + "\n")
		}

		// print published sources
		sb.WriteString("\n\nSources:\n")
		rt := make([]string, 0)
		for src, fd := range n.RouterState.Sources {
			rt = append(rt, fmt.Sprintf(" - %s: m=%d, seqno=%d", src, fd.Metric, fd.Seqno))
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		// print advertised prefixes
		sb.WriteString("\n\nAdvertised Prefixes:\n")
		rt = make([]string, 0)
		for prefix, adv := range n.RouterState.Advertised {
			timeRem := time.Until(adv.Expiry)
			if timeRem > time.Hour*24 {
				rt = append(rt, fmt.Sprintf(" - %s expires never nh %s metric %d", prefix, adv.NodeId, adv.MetricFn()))
			} else {
				rt = append(rt, fmt.Sprintf(" - %s expires %.2fs nh %s metric %d", prefix, timeRem.Seconds(), adv.NodeId, adv.MetricFn()))
			}
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		// print route table
		sb.WriteString("\n\nRoute Table:\n")
		rt = make([]string, 0)
		for svc, route := range n.RouterState.Routes {
			rt = append(rt, fmt.Sprintf(" - %s via %s", svc, route))
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		// print forward table
		sb.WriteString("\n\nForward Table:\n")
		rt = make([]string, 0)
		for prefix, route := range n.Router.ForwardTable.All() {
			rt = append(rt, fmt.Sprintf(" - %s via %s", prefix, route.Nh))
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		// print exit table
		sb.WriteString("\n\nExit Table:\n")
		rt = make([]string, 0)
		for prefix, route := range n.Router.ExitTable.All() {
			rt = append(rt, fmt.Sprintf(" - %s via %s", prefix, route.Nh))
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		_, err = rw.WriteString(sb.String())
		if err != nil {
			return err
		}
		err = rw.WriteByte(0)
		if err != nil {
			return err
		}
		return rw.Flush()
	case "trace\n":
		if !state.DBG_trace_tc {
			return fmt.Errorf("trace mode is not enabled")
		}
		ctx, cancel := context.WithCancel(context.Background())
		t := n.Trace
		go func() {
			_, _ = rw.ReadByte() // wait for EOF
			cancel()
		}()
		ch := make(chan interface{})
		t.Register(ch)
		defer t.Unregister(ch)
		for {
			select {
			case <-ctx.Done():
				return nil
			case msg := <-ch:
				if str, ok := msg.(string); ok {
					_, err := rw.WriteString(str)
					if err != nil {
						return err
					}
					err = rw.Flush()
					if err != nil {
						return err
					}
				}
			}
		}
	default:
		return fmt.Errorf("unknown command %s", cmd)
	}
}
