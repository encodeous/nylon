//go:build e2e

package e2e

import (
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/encodeous/nylon/e2e/machine"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/testcontainers/testcontainers-go"
)

// StartMachines is the portable replacement for StartNodes. Existing fixtures
// may continue using StartNodes/Nodes until their OS-specific operations migrate.
func (h *Harness) StartMachines(specs ...NodeSpec) {
	h.t.Helper()
	names := make(map[string]bool)
	ips := make(map[string]bool)
	h.mu.Lock()
	for name := range h.Machines {
		names[name] = true
	}
	h.mu.Unlock()
	needsWindows := false
	for i := range specs {
		spec := &specs[i]
		if spec.OS == "" {
			spec.OS = machine.OS(os.Getenv("NYLON_E2E_MACHINE"))
			if spec.OS == "" {
				spec.OS = machine.Linux
			}
		}
		if spec.OS != machine.Linux && spec.OS != machine.Windows {
			h.t.Fatalf("unsupported machine OS %q (use linux or windows)", spec.OS)
		}
		needsWindows = needsWindows || spec.OS == machine.Windows
		if spec.Name == "" || names[spec.Name] {
			h.t.Fatalf("empty or duplicate machine name %q", spec.Name)
		}
		names[spec.Name] = true
		if spec.IP != "" {
			if ips[spec.IP] {
				h.t.Fatalf("duplicate machine data IP %q", spec.IP)
			}
			ips[spec.IP] = true
		}
	}
	h.t.Log("Running machine preflight")
	prepared, err := h.preflight(needsWindows)
	if err != nil {
		h.t.Fatalf("machine preflight: %v", err)
	}
	errs := make(chan error, len(specs))
	var wg sync.WaitGroup
	for _, spec := range specs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.startMachine(spec, prepared); err != nil {
				errs <- fmt.Errorf("machine %s: %w", spec.Name, err)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		h.t.Error(err)
	}
	if h.t.Failed() {
		h.t.FailNow()
	}
}

func (h *Harness) startMachine(spec NodeSpec, prepared windowsPrerequisites) error {
	osType := spec.OS
	var m machine.Machine
	var opts machine.NylonOptions
	opts.ExtraArgs = spec.ExtraArgs
	var err error
	h.t.Logf("Starting %s machine %s at %s", osType, spec.Name, spec.IP)
	switch osType {
	case machine.Linux:
		m, err = machine.NewDocker(h.ctx, machine.DockerConfig{Name: spec.Name, Image: ImageName, Network: h.Network.Name, IP: spec.IP})
	case machine.Windows:
		addr, parseErr := netip.ParseAddr(spec.IP)
		if parseErr != nil || !addr.Is4() || addr.As4()[3] < 2 || addr.As4()[3] >= 128 {
			return fmt.Errorf("Windows requires an explicit data IP in hosts .2 through .127")
		}
		cfg := prepared.config
		opts.Binary, opts.WintunDLL = prepared.binary, prepared.dll
		cfg.Name, cfg.IP = spec.Name, spec.IP
		m, err = machine.NewWindows(h.ctx, cfg)
		opts.StartupTimeout = time.Minute
	}
	if err != nil {
		return err
	}
	h.t.Logf("Machine %s is ready; installing Nylon and configuration", spec.Name)
	// Register immediately, so config/upload/start failures still get cleanup.
	h.mu.Lock()
	h.Machines[spec.Name] = m
	h.mu.Unlock()
	centralBytes, err := os.ReadFile(spec.CentralConfigPath)
	if err != nil {
		return err
	}
	localBytes, err := os.ReadFile(spec.NodeConfigPath)
	if err != nil {
		return err
	}
	var central state.CentralCfg
	var local state.LocalCfg
	if err := yaml.Unmarshal(centralBytes, &central); err != nil {
		return err
	}
	if err := yaml.Unmarshal(localBytes, &local); err != nil {
		return err
	}
	p, err := machine.RunNylon(h.ctx, m, central, local, opts)
	if err != nil {
		return err
	}
	h.t.Logf("Nylon initialized on %s", spec.Name)
	h.mu.Lock()
	h.processes[spec.Name] = p
	h.mu.Unlock()
	h.followProcess(spec.Name, p)
	return nil
}

func (h *Harness) followProcess(name string, p machine.Process) {
	h.logWG.Add(1)
	go func() {
		defer h.logWG.Done()
		var offsets [2]int
		consumer := UnifiedLogConsumer{Node: name, Manager: h.LogManager}
		for {
			r, exited, err := p.Poll(h.ctx)
			if err != nil {
				if h.ctx.Err() == nil {
					h.t.Logf("read logs for %s: %v", name, err)
				}
				return
			}
			for i, data := range []string{r.Stdout, r.Stderr} {
				chunk := data[offsets[i]:]
				end := strings.LastIndexByte(chunk, '\n') + 1
				if exited {
					end = len(chunk)
				}
				// Preserve per-line matching, including Windows logs on stderr.
				for _, line := range strings.SplitAfter(chunk[:end], "\n") {
					if line == "" {
						continue
					}
					consumer.Accept(testcontainers.Log{LogType: "stdout", Content: []byte(line)})
					if i == 1 {
						h.LogManager.Accept(name, SourceStderr, StripAnsi(line))
					}
				}
				offsets[i] += end
			}
			if exited {
				return
			}
			select {
			case <-h.ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}()
}

func (h *Harness) Ping(name string, addr netip.Addr, opts machine.PingOptions) (machine.Result, error) {
	h.mu.Lock()
	m := h.Machines[name]
	h.mu.Unlock()
	if m == nil {
		return machine.Result{}, fmt.Errorf("machine %q not found", name)
	}
	return machine.Ping(h.ctx, m, addr, opts)
}
