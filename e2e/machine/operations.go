package machine

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

type NylonOptions struct {
	// Host paths prepared by preflight for Windows; Linux uses its Docker image.
	Binary         string
	WintunDLL      string
	ExtraArgs      []string
	StartupTimeout time.Duration
}

// RunNylon installs config and optional artifacts, launches Nylon, and waits for
// initialization. Routing convergence is left to the scenario's assertions.
func RunNylon(ctx context.Context, m Machine, central state.CentralCfg, local state.LocalCfg, opts NylonOptions) (_ Process, err error) {
	// Bound setup as well as initialization: an interrupted WinRM upload must
	// not hang a test until go test's hard timeout (which bypasses cleanup).
	ctx, cancelSetup := context.WithTimeout(ctx, 5*time.Minute)
	defer cancelSetup()
	centralData, err := yaml.Marshal(central)
	if err != nil {
		return nil, err
	}
	localData, err := yaml.Marshal(local)
	if err != nil {
		return nil, err
	}
	for name, data := range map[string][]byte{"central.yaml": centralData, "node.yaml": localData} {
		if err := m.WriteFile(ctx, guestPath(m, name), data); err != nil {
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}
	binary := "/usr/local/bin/nylon"
	if m.Info().OS == Windows {
		binary = guestPath(m, "nylon.exe")
		if opts.Binary == "" || opts.WintunDLL == "" {
			return nil, fmt.Errorf("Windows requires NylonOptions.Binary and WintunDLL")
		}
		for name, source := range map[string]string{"nylon.exe": opts.Binary, "wintun.dll": opts.WintunDLL} {
			if err := upload(ctx, m, source, guestPath(m, name)); err != nil {
				return nil, err
			}
		}
	}
	args := []string{"run", "-v", "-w", "--dbg-trace-tc", "--config", guestPath(m, "central.yaml"), "--node", guestPath(m, "node.yaml")}
	args = append(args, opts.ExtraArgs...)
	p, err := m.Start(ctx, Command{Path: binary, Args: args, Dir: m.Info().WorkDir})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = p.Stop(cleanupCtx)
		}
	}()
	timeout := opts.StartupTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var output Result
	for {
		var exited bool
		output, exited, err = p.Poll(waitCtx)
		if err != nil {
			return nil, fmt.Errorf("read Nylon startup output: %w", err)
		}
		if exited {
			return nil, fmt.Errorf("Nylon exited during startup (%d): %s\n%s", output.ExitCode, output.Stdout, output.Stderr)
		}
		if strings.Contains(output.Stdout+output.Stderr, "Nylon has been initialized") {
			if m.Info().OS == Windows && !local.NoTun {
				name := local.InterfaceName
				if name == "" {
					name = "nylon"
				}
				// Wintun appears after the machine's data-NIC firewall setup.
				// Allow the scenario's inner packets on this disposable interface.
				script := "New-NetFirewallRule -DisplayName 'Nylon e2e overlay inbound' -Direction Inbound -InterfaceAlias " + psQuote(name) + " -Action Allow -Profile Any | Out-Null\n" +
					"New-NetFirewallRule -DisplayName 'Nylon e2e overlay outbound' -Direction Outbound -InterfaceAlias " + psQuote(name) + " -Action Allow -Profile Any | Out-Null"
				if _, err := m.Run(waitCtx, Command{Path: "powershell.exe", Args: []string{"-NoProfile", "-NonInteractive", "-Command", script}}); err != nil {
					return nil, fmt.Errorf("configure Windows overlay firewall: %w", err)
				}
			}
			return p, nil
		}
		if err := pause(waitCtx, 250*time.Millisecond); err != nil {
			return nil, fmt.Errorf("waiting for Nylon initialization: %w\n%s\n%s", err, output.Stdout, output.Stderr)
		}
	}
}

func upload(ctx context.Context, m Machine, src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := m.WriteFile(ctx, dst, data); err != nil {
		return fmt.Errorf("upload %s: %w", dst, err)
	}
	return nil
}

type PingOptions struct {
	Count   int
	Timeout time.Duration // Per reply.
}

// Ping executes the guest's ping against an IP, exercising its real system
// routing and TUN. Failure is determined by replies, including on Windows where
// ping.exe may otherwise return success for a destination-unreachable reply.
func Ping(ctx context.Context, m Machine, addr netip.Addr, opts PingOptions) (Result, error) {
	if !addr.IsValid() {
		return Result{}, fmt.Errorf("invalid ping address")
	}
	if opts.Count == 0 {
		opts.Count = 3
	}
	if opts.Timeout == 0 {
		opts.Timeout = 2 * time.Second
	}
	if opts.Count < 1 || opts.Timeout < 0 {
		return Result{}, fmt.Errorf("invalid ping options")
	}
	// Include intervals and command startup, while respecting an earlier caller deadline.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(opts.Count)*(opts.Timeout+time.Second)+15*time.Second)
	defer cancel()
	if m.Info().OS == Windows {
		// .NET Ping is independent of the language of the Windows installation and
		// gives an explicit IPStatus for each reply. It uses the guest network stack.
		script := fmt.Sprintf(`$p = New-Object System.Net.NetworkInformation.Ping
$failed = $false
try {
  for ($i = 0; $i -lt %d; $i++) {
    $r = $p.Send(%s, %d)
    Write-Output ($r.Status.ToString() + ' ' + $r.RoundtripTime + 'ms')
    if ($r.Status -ne [System.Net.NetworkInformation.IPStatus]::Success) { $failed = $true }
  }
} finally { $p.Dispose() }
if ($failed) { exit 1 }`, opts.Count, psQuote(addr.String()), (opts.Timeout+time.Millisecond-1)/time.Millisecond)
		return m.Run(ctx, Command{Path: "powershell.exe", Args: []string{"-NoProfile", "-NonInteractive", "-Command", script}})
	}
	// With both count and deadline, iputils exits nonzero unless all requested
	// replies arrive. This matches the Windows IPStatus checks above.
	deadline := int(math.Ceil(float64(opts.Count) * (opts.Timeout + time.Second).Seconds()))
	args := []string{"-n", "-c", strconv.Itoa(opts.Count), "-W", strconv.FormatFloat(opts.Timeout.Seconds(), 'f', 3, 64), "-w", strconv.Itoa(deadline)}
	args = append(args, addr.String())
	return m.Run(ctx, Command{Path: "ping", Args: args})
}
