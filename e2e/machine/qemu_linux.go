package machine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type QEMUWindows struct {
	info     Info
	port     int
	cfg      WindowsConfig
	dir, tap string
	cmd      *exec.Cmd
	done     chan struct{}
	waitErr  error
	log      *os.File
}

// NewWindows boots an isolated UEFI guest. Its TAP joins the test bridge; a
// separate user-mode management NIC forwards WinRM to localhost. Neither test
// routes nor link failures can strand the management connection.
// BaseImage must contain the standalone qcow2 prepared by preflight.
func NewWindows(ctx context.Context, cfg WindowsConfig) (_ *QEMUWindows, err error) {
	addr, err := netip.ParseAddr(cfg.IP)
	if err != nil || !addr.Is4() {
		return nil, fmt.Errorf("Windows machine requires a valid IPv4 data address")
	}
	prefix, err := netip.ParsePrefix(cfg.Subnet)
	if err != nil || !prefix.Contains(addr) {
		return nil, fmt.Errorf("Windows data IP must belong to the test subnet")
	}
	if cfg.Bridge == "" || cfg.HostHelperImage == "" {
		return nil, fmt.Errorf("Windows requires a local Docker bridge and host helper image")
	}
	if cfg.Accelerator != "kvm" && cfg.Accelerator != "tcg" {
		return nil, fmt.Errorf("QEMU accelerator must be kvm or tcg")
	}
	if cfg.MemoryMiB < 1 || cfg.CPUs < 1 || cfg.BootTimeout <= 0 {
		return nil, fmt.Errorf("invalid Windows VM resources or boot timeout")
	}
	if _, err := exec.LookPath("qemu-system-x86_64"); err != nil {
		return nil, err
	}
	if cfg.Accelerator == "kvm" {
		kvm, err := os.OpenFile("/dev/kvm", os.O_RDWR, 0)
		if err != nil {
			return nil, fmt.Errorf("KVM unavailable (use NYLON_E2E_QEMU_ACCEL=tcg explicitly for emulation): %w", err)
		}
		kvm.Close()
	}
	if cfg.BaseImage == "" {
		return nil, fmt.Errorf("Windows base image must be prepared by preflight")
	}
	if err := os.MkdirAll(cfg.WorkDir, 0700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(cfg.WorkDir, "windows-")
	if err != nil {
		return nil, err
	}
	w := &QEMUWindows{cfg: cfg, dir: dir, info: Info{OS: Windows, IP: cfg.IP, WorkDir: `C:\nylon-e2e`}}
	defer func() {
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			err = errors.Join(err, w.Close(cleanupCtx))
		}
	}()
	overlay := filepath.Join(dir, "disk.qcow2")
	if err := CreateOverlay(ctx, cfg.BaseImage, overlay); err != nil {
		return nil, err
	}
	vars, err := os.ReadFile(cfg.UEFIVars)
	if err != nil {
		return nil, fmt.Errorf("read UEFI variable template: %w", err)
	}
	varsPath := filepath.Join(dir, "uefi-vars.fd")
	if err := os.WriteFile(varsPath, vars, 0600); err != nil {
		return nil, err
	}
	if _, err := os.Stat(cfg.UEFICode); err != nil {
		return nil, fmt.Errorf("read UEFI firmware: %w", err)
	}
	var random [5]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	w.tap = "ny" + hex.EncodeToString(random[:]) // Linux interface names <= 15 bytes.
	macSuffix := fmt.Sprintf("%02x:%02x:%02x", random[0], random[1], random[2])
	dataMAC, mgmtMAC := "52:54:00:"+macSuffix, "52:54:01:"+macSuffix
	if err := w.hostNetwork(ctx, `ip tuntap add dev "$1" mode tap user "$3" && ip link set dev "$1" master "$2" && ip link set dev "$1" up`, w.tap, cfg.Bridge, strconv.Itoa(os.Getuid())); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	w.port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	w.log, err = os.OpenFile(filepath.Join(dir, "qemu.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	cpu := "max"
	if cfg.Accelerator == "kvm" {
		cpu = "host"
	}
	args := []string{
		"-name", cfg.Name, "-machine", "q35", "-accel", cfg.Accelerator, "-cpu", cpu,
		"-m", strconv.Itoa(cfg.MemoryMiB), "-smp", strconv.Itoa(cfg.CPUs),
		"-display", "none", "-monitor", "none", "-serial", "file:" + qemuValue(filepath.Join(dir, "serial.log")),
		"-drive", "if=pflash,format=raw,readonly=on,file=" + qemuValue(cfg.UEFICode),
		"-drive", "if=pflash,format=raw,file=" + qemuValue(varsPath),
		"-drive", "if=ide,format=qcow2,file=" + qemuValue(overlay),
		"-netdev", "tap,id=data,ifname=" + w.tap + ",script=no,downscript=no",
		"-device", cfg.NICModel + ",netdev=data,mac=" + dataMAC,
		"-netdev", fmt.Sprintf("user,id=mgmt,restrict=on,net=192.168.252.0/24,hostfwd=tcp:127.0.0.1:%d-:5985", w.port),
		"-device", cfg.NICModel + ",netdev=mgmt,mac=" + mgmtMAC,
	}
	w.cmd = exec.Command("qemu-system-x86_64", args...)
	// Do not leave VMs running if go test is killed or reaches its hard timeout.
	w.cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	w.cmd.Stdout, w.cmd.Stderr = w.log, w.log
	if err := w.cmd.Start(); err != nil {
		return nil, err
	}
	w.done = make(chan struct{})
	go func() { w.waitErr = w.cmd.Wait(); close(w.done) }()
	bootCtx, cancel := context.WithTimeout(ctx, cfg.BootTimeout)
	defer cancel()
	var lastErr error
	for {
		select {
		case <-w.done:
			return nil, fmt.Errorf("QEMU exited during boot: %v (see %s)", w.waitErr, filepath.Join(dir, "qemu.log"))
		default:
		}
		probeCtx, probeCancel := context.WithTimeout(bootCtx, 20*time.Second)
		_, lastErr = w.runPS(probeCtx, "[Console]::Out.Write('ready')", "")
		probeCancel()
		if lastErr == nil {
			break
		}
		if err := pause(bootCtx, time.Second); err != nil {
			return nil, fmt.Errorf("Windows WinRM readiness: %w; last error: %v (see %s)", err, lastErr, filepath.Join(dir, "qemu.log"))
		}
	}
	// Select the data adapter by MAC, never by an unstable Windows adapter name.
	// It has no default gateway, leaving management replies on the management NIC.
	mac := strings.ReplaceAll(dataMAC, ":", "")
	script := `$adapter = Get-NetAdapter | Where-Object { ($_.MacAddress -replace '[:-]', '') -eq ` + psQuote(mac) + ` }
if (!$adapter) { throw 'QEMU test data adapter not found' }
$idx = $adapter.ifIndex
Set-NetIPInterface -InterfaceIndex $idx -AddressFamily IPv4 -Dhcp Disabled
Get-NetIPAddress -InterfaceIndex $idx -AddressFamily IPv4 -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue
New-NetIPAddress -InterfaceIndex $idx -IPAddress ` + psQuote(cfg.IP) + ` -PrefixLength ` + strconv.Itoa(prefix.Bits()) + ` | Out-Null
New-NetFirewallRule -DisplayName 'Nylon e2e data network' -Direction Inbound -InterfaceAlias $adapter.Name -Action Allow -Profile Any | Out-Null
New-NetFirewallRule -DisplayName 'Nylon e2e data outbound' -Direction Outbound -InterfaceAlias $adapter.Name -Action Allow -Profile Any | Out-Null
[void][IO.Directory]::CreateDirectory(` + psQuote(w.info.WorkDir) + `)`
	if _, err := w.runPS(bootCtx, script, ""); err != nil {
		return nil, fmt.Errorf("configure Windows test network: %w", err)
	}
	return w, nil
}

func qemuValue(s string) string { return strings.ReplaceAll(s, ",", ",,") }

func (w *QEMUWindows) Info() Info { return w.info }

// A local privileged Docker helper configures the host TAP without requiring
// sudo in the Go test runner. The existing debug image already has iproute2.
func (w *QEMUWindows) hostNetwork(ctx context.Context, script string, args ...string) error {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:              w.cfg.HostHelperImage,
			Entrypoint:         []string{"sh", "-c", script, "machine"},
			Cmd:                args,
			HostConfigModifier: func(h *container.HostConfig) { h.NetworkMode = "host"; h.Privileged = true },
			WaitingFor:         wait.ForExit().WithExitTimeout(30 * time.Second),
		}, Started: true,
	})
	if c != nil {
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = c.Terminate(cleanupCtx)
		}()
	}
	if err != nil {
		return fmt.Errorf("host network helper: %w", err)
	}
	state, err := c.State(ctx)
	if err != nil {
		return err
	}
	if state.ExitCode != 0 {
		logs, logErr := c.Logs(ctx)
		var output []byte
		if logErr == nil {
			defer logs.Close()
			output, _ = io.ReadAll(logs)
		}
		return fmt.Errorf("host network helper exited %d: %s", state.ExitCode, output)
	}
	return nil
}

func (w *QEMUWindows) Close(ctx context.Context) error {
	var closeErr error
	if w.done != nil {
		select {
		case <-w.done:
		default:
			// Stop QEMU before removing its writable disk. A canceled cleanup can be retried.
			if err := w.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				closeErr = errors.Join(closeErr, err)
			}
			select {
			case <-w.done:
			case <-ctx.Done():
				return errors.Join(closeErr, ctx.Err())
			}
		}
	}
	if w.log != nil {
		closeErr = errors.Join(closeErr, w.log.Close())
		w.log = nil
	}
	if w.tap != "" {
		err := w.hostNetwork(ctx, `if ip link show dev "$1" >/dev/null 2>&1; then ip link delete dev "$1"; fi`, w.tap)
		closeErr = errors.Join(closeErr, err)
		if err == nil {
			w.tap = ""
		}
	}
	// Keep diagnostics, removing only the disposable disk and firmware vars.
	for _, name := range []string{"disk.qcow2", "uefi-vars.fd"} {
		if err := os.Remove(filepath.Join(w.dir, name)); err != nil && !os.IsNotExist(err) {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}
