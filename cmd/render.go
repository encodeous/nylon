package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"google.golang.org/protobuf/encoding/protojson"
)

type paletteValues struct {
	header  func(string) string
	section func(string) string
	column  func(string) string
	key     func(string) string
	value   func(string) string
	good    func(string) string
	warn    func(string) string
	bad     func(string) string
	muted   func(string) string
}

func palette(enabled bool) paletteValues {
	paint := func(code string) func(string) string {
		if !enabled {
			return func(s string) string { return s }
		}
		return func(s string) string { return "\x1b[" + code + "m" + s + "\x1b[0m" }
	}
	return paletteValues{
		header:  paint("1;36"),
		section: paint("1;39;49"),
		column:  paint("0;33"),
		key:     paint("1;34"),
		value:   paint("0;37;49"),
		good:    paint("32"),
		warn:    paint("33"),
		bad:     paint("31"),
		muted:   paint("2"),
	}
}

func printKV(p paletteValues, indent int, key, value string) {
	fmt.Printf("%s%s: %s\n", strings.Repeat("  ", indent), p.key(key), value)
}

func printTable(p paletteValues, indent int, headers []string, rows [][]string) {
	if len(rows) == 0 {
		fmt.Println(strings.Repeat("  ", indent) + p.muted("none"))
		return
	}
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && visibleLen(cell) > widths[i] {
				widths[i] = visibleLen(cell)
			}
		}
	}
	prefix := strings.Repeat("  ", indent)
	printRow := func(cells []string, paint func(string) string) {
		fmt.Print(prefix)
		for i, cell := range cells {
			if i > 0 {
				fmt.Print("  ")
			}
			fmt.Print(paint(padRight(cell, widths[i])))
		}
		fmt.Println()
	}
	printRow(headers, p.column)
	for _, row := range rows {
		printRow(row, p.value)
	}
}

func printCommaList(p paletteValues, indent int, values []string) {
	prefix := strings.Repeat("  ", indent)
	if len(values) == 0 {
		fmt.Println(prefix + p.muted("none"))
		return
	}
	fmt.Println(prefix + p.value(strings.Join(values, ", ")))
}

func visibleLen(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		n++
	}
	return n
}

func padRight(s string, width int) string {
	if extra := width - visibleLen(s); extra > 0 {
		return s + strings.Repeat(" ", extra)
	}
	return s
}

func metricText(p paletteValues, metric uint32) string {
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

func formatHandshake(unix int64) string {
	if unix <= 0 {
		return "never"
	}
	return time.Since(time.Unix(0, unix)).Truncate(time.Second).String() + " ago"
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

func formatDurationNs(ns int64) string {
	if ns <= 0 {
		return "-"
	}
	return time.Duration(ns).String()
}

func printJSON(resp *protocol.IpcResponse) {
	m := protojson.MarshalOptions{Indent: "  ", EmitUnpopulated: true}
	data, _ := m.Marshal(resp)
	fmt.Println(string(data))
}
