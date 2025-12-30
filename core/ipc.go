package core

import (
	"bufio"
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
	return res, nil
}

func HandleNylonIPCGet(s *state.State, rw *bufio.ReadWriter) error {
	cmd, err := rw.ReadString('\n')
	if err != nil {
		return err
	}
	sb := strings.Builder{}
	switch cmd {
	case "inspect\n":
		// print neighbours
		sb.WriteString("Neighbours:\n")
		for _, n := range s.Neighbours {
			sb.WriteString(fmt.Sprintf(" - %s\n", n.Id))
			met := state.INF
			if n.BestEndpoint() != nil {
				met = n.BestEndpoint().Metric()
			}
			sb.WriteString(fmt.Sprintf("   Metric: %d\n", met))
			sb.WriteString(fmt.Sprintf("   Published Routes:\n"))
			rt := make([]string, 0)
			if len(n.Routes) == 0 {
				rt = append(rt, "    (none)")
			}
			for _, r := range n.Routes {
				rt = append(rt, fmt.Sprintf("    - %s", r.String()))
			}
			slices.Sort(rt)
			sb.WriteString(strings.Join(rt, "\n") + "\n")
		}

		// print published sources
		sb.WriteString("\n\nSources:\n")
		rt := make([]string, 0)
		for src, fd := range s.Sources {
			rt = append(rt, fmt.Sprintf(" - %s: m=%d, seqno=%d", src, fd.Metric, fd.Seqno))
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		// print advertised prefixes
		sb.WriteString("\n\nAdvertised Prefixes:\n")
		rt = make([]string, 0)
		for prefix, adv := range s.Advertised {
			timeRem := adv.Expiry.Sub(time.Now())
			if timeRem > time.Hour*24 {
				rt = append(rt, fmt.Sprintf(" - %s expires never nh %s", prefix, adv.NodeId))
			} else {
				rt = append(rt, fmt.Sprintf(" - %s expires %.2fs nh %s", prefix, timeRem.Seconds(), adv.NodeId))
			}
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")

		// print route table
		sb.WriteString("\n\nRoute Table:\n")
		rt = make([]string, 0)
		for svc, route := range s.Routes {
			rt = append(rt, fmt.Sprintf(" - %s via %s", svc, route))
		}
		slices.Sort(rt)
		sb.WriteString(strings.Join(rt, "\n") + "\n")
		sb.WriteRune(0)
		_, err = rw.WriteString(sb.String())
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown command %s", cmd)
	}
	return nil
}
