package machine

import (
	"context"
	"fmt"
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
	Binary    string
	WintunDLL string
	ExtraArgs []string
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
		for name, source := range map[string]string{"nylon.exe": opts.Binary, "wintun.dll": opts.WintunDLL} {
			if err := upload(ctx, m, source, guestPath(m, name)); err != nil {
				return nil, err
			}
		}
	}
	args := []string{"run", "-v", "-w", "--dbg-trace-tc", "--config", guestPath(m, "central.yaml"), "--node", guestPath(m, "node.yaml")}
	args = append(args, opts.ExtraArgs...)
	p, err := m.Start(ctx, Command{Path: binary, Args: args})
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
	var output Result
	for {
		var exited bool
		output, exited, err = p.Poll(ctx)
		if err != nil {
			return nil, fmt.Errorf("read Nylon startup output: %w", err)
		}
		if exited {
			return nil, fmt.Errorf("Nylon exited during startup (%d): %s\n%s", output.ExitCode, output.Stdout, output.Stderr)
		}
		if strings.Contains(output.Stdout+output.Stderr, "Nylon has been initialized") {
			return p, nil
		}
		if err := pause(ctx, 250*time.Millisecond); err != nil {
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

// Ping requires all requested replies on both platforms.
func Ping(ctx context.Context, m Machine, addr netip.Addr, count int) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(count*3+15)*time.Second)
	defer cancel()
	if m.Info().OS == Windows {
		script := fmt.Sprintf(`$p = New-Object System.Net.NetworkInformation.Ping
$failed = $false
for ($i = 0; $i -lt %d; $i++) {
  $r = $p.Send('%s', 2000)
  Write-Output ($r.Status.ToString())
  if ($r.Status -ne [System.Net.NetworkInformation.IPStatus]::Success) { $failed = $true }
}
if ($failed) { exit 1 }`, count, addr)
		return m.Run(ctx, Command{Path: "powershell.exe", Args: []string{"-NoProfile", "-NonInteractive", "-Command", script}})
	}
	return m.Run(ctx, Command{Path: "ping", Args: []string{"-n", "-c", strconv.Itoa(count), "-W", "2", "-w", strconv.Itoa(count * 3), addr.String()}})
}
