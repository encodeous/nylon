package machine

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type WindowsConfig struct{ IP, Bridge, Dir, Image string }

type QEMUWindows struct {
	info     Info
	port     int
	dir, tap string
	cmd      *exec.Cmd
	log      *os.File
}

// The CI host provides Docker, KVM and Ubuntu's OVMF 4M firmware.
func NewWindows(ctx context.Context, cfg WindowsConfig) (_ *QEMUWindows, err error) {
	ip := netip.MustParseAddr(cfg.IP).As4()
	if err := os.MkdirAll(cfg.Dir, 0755); err != nil {
		return nil, err
	}
	w := &QEMUWindows{dir: cfg.Dir, tap: fmt.Sprintf("ny-%x", ip), info: Info{OS: Windows, IP: cfg.IP, WorkDir: `C:\nylon-e2e`}}
	defer func() {
		if err != nil {
			_ = w.Close(context.Background())
		}
	}()
	overlay := filepath.Join(cfg.Dir, "disk.qcow2")
	if out, err := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", cfg.Image, overlay).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create overlay: %w: %s", err, out)
	}
	vars, err := os.ReadFile("/usr/share/OVMF/OVMF_VARS_4M.fd")
	if err != nil {
		return nil, err
	}
	varsPath := filepath.Join(cfg.Dir, "uefi-vars.fd")
	if err := os.WriteFile(varsPath, vars, 0600); err != nil {
		return nil, err
	}
	dataMAC := fmt.Sprintf("52:54:%02x:%02x:%02x:%02x", ip[0], ip[1], ip[2], ip[3])
	if err := w.hostNetwork(ctx, `ip tuntap add dev "$1" mode tap user "$3"; ip link set dev "$1" master "$2"; ip link set dev "$1" up`, w.tap, cfg.Bridge, strconv.Itoa(os.Getuid())); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	w.port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	w.log, err = os.Create(filepath.Join(cfg.Dir, "qemu.log"))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("qemu-system-x86_64",
		"-machine", "q35", "-accel", "kvm", "-cpu", "host", "-m", "2048", "-smp", "2",
		"-display", "none", "-monitor", "none", "-serial", "file:"+filepath.Join(cfg.Dir, "serial.log"),
		"-drive", "if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd",
		"-drive", "if=pflash,format=raw,file="+varsPath,
		"-drive", "if=ide,format=qcow2,file="+overlay,
		"-netdev", "tap,id=data,ifname="+w.tap+",script=no,downscript=no",
		"-device", "virtio-net-pci,netdev=data,mac="+dataMAC,
		"-netdev", fmt.Sprintf("user,id=mgmt,restrict=on,net=192.168.252.0/24,hostfwd=tcp:127.0.0.1:%d-:5985", w.port),
		"-device", "virtio-net-pci,netdev=mgmt,mac=52:54:00:12:34:56")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	cmd.Stdout, cmd.Stderr = w.log, w.log
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	w.cmd = cmd
	bootCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	for {
		if _, err := w.runPS(bootCtx, "'ready'", ""); err == nil {
			break
		}
		if err := pause(bootCtx, time.Second); err != nil {
			return nil, err
		}
	}
	script := `$adapter = Get-NetAdapter | Where-Object { ($_.MacAddress -replace '[:-]', '') -eq ` + psQuote(strings.ReplaceAll(dataMAC, ":", "")) + ` }
Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -Dhcp Disabled
Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false
New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress ` + psQuote(cfg.IP) + ` -PrefixLength 24 | Out-Null
Set-NetFirewallProfile -Profile Domain,Private,Public -Enabled False
[void][IO.Directory]::CreateDirectory('C:\nylon-e2e')`
	if _, err := w.runPS(bootCtx, script, ""); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *QEMUWindows) Info() Info { return w.info }

func (w *QEMUWindows) hostNetwork(ctx context.Context, script string, args ...string) error {
	argv := append([]string{"run", "--rm", "--privileged", "--network", "host", "--entrypoint", "sh", "nylon-debug:latest", "-ec", script, "machine"}, args...)
	out, err := exec.CommandContext(ctx, "docker", argv...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("host network: %w: %s", err, out)
	}
	return nil
}

func (w *QEMUWindows) Close(ctx context.Context) error {
	if w.cmd != nil {
		_ = w.cmd.Process.Kill()
		_ = w.cmd.Wait()
	}
	if w.log != nil {
		_ = w.log.Close()
	}
	err := w.hostNetwork(ctx, `ip link delete dev "$1"`, w.tap)
	_ = os.Remove(filepath.Join(w.dir, "disk.qcow2"))
	_ = os.Remove(filepath.Join(w.dir, "uefi-vars.fd"))
	return err
}
