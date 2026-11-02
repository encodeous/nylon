//go:build e2e && linux

package e2e

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/encodeous/nylon/e2e/machine"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

// StartMachines is the portable replacement for StartNodes. Existing fixtures
// may continue using StartNodes/Nodes until their OS-specific operations migrate.
func (h *Harness) StartMachines(specs ...NodeSpec) {
	h.t.Helper()
	needsWindows := false
	for i := range specs {
		spec := &specs[i]
		if spec.OS == "" {
			spec.OS = machine.OS(os.Getenv("NYLON_E2E_MACHINE"))
			if spec.OS == "" {
				spec.OS = machine.Linux
			}
		}
		needsWindows = needsWindows || spec.OS == machine.Windows
	}

	h.t.Log("Running machine preflight")
	var prepared windowsPrerequisites
	if needsWindows {
		var err error
		prepared, err = preflight(h.ctx, h.RootDir)
		if err != nil {
			h.t.Fatal(err)
		}
	}
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Go(func() { errs[i] = h.startMachine(spec, prepared) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		h.t.Fatal(err)
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
		m, err = machine.NewDocker(h.ctx, machine.DockerConfig{Name: spec.Name, Network: h.Network.Name, IP: spec.IP})
	case machine.Windows:
		opts.Binary, opts.WintunDLL = prepared.binary, prepared.dll
		m, err = machine.NewWindows(h.ctx, machine.WindowsConfig{
			IP: spec.IP, Bridge: "br-" + h.Network.ID[:12], Image: prepared.image,
			Dir: filepath.Join(h.RootDir, "workdir", "e2e", "vms", h.t.Name(), spec.Name),
		})
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
		writers := [2]managerWriter{}
		for i := range writers {
			writers[i] = managerWriter{node: name, source: SourceStdout, manager: h.LogManager}
		}
		for {
			r, exited, err := p.Poll(h.ctx)
			if err != nil {
				return
			}
			for i, data := range []string{r.Stdout, r.Stderr} {
				_, _ = writers[i].Write([]byte(data[offsets[i]:]))
				offsets[i] = len(data)
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

func (h *Harness) Ping(name string, addr netip.Addr, count int) (machine.Result, error) {
	h.mu.Lock()
	m := h.Machines[name]
	h.mu.Unlock()
	return machine.Ping(h.ctx, m, addr, count)
}
