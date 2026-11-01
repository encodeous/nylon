package machine

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

type DockerConfig struct {
	Name    string
	Image   string
	Network string
	IP      string
}

type Docker struct {
	info      Info
	Container testcontainers.Container
	provider  *testcontainers.DockerProvider
}

func NewDocker(ctx context.Context, cfg DockerConfig) (*Docker, error) {
	if cfg.Image == "" || cfg.Network == "" {
		return nil, fmt.Errorf("Docker machine requires an image and network")
	}
	if cfg.IP != "" {
		if _, err := netip.ParseAddr(cfg.IP); err != nil {
			return nil, err
		}
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          cfg.Image,
			Entrypoint:     []string{"/bin/sh", "-c", "exec sleep infinity"},
			Networks:       []string{cfg.Network},
			NetworkAliases: map[string][]string{cfg.Network: {cfg.Name}},
			HostConfigModifier: func(h *container.HostConfig) {
				h.Privileged = true
				h.CapAdd = []string{"NET_ADMIN"}
			},
			EndpointSettingsModifier: func(endpoints map[string]*network.EndpointSettings) {
				if cfg.IP != "" && endpoints[cfg.Network] != nil {
					endpoints[cfg.Network].IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(cfg.IP)}
				}
			},
		},
		Started: true,
	})
	if err != nil {
		if c != nil {
			_ = c.Terminate(context.Background())
		}
		return nil, err
	}
	if cfg.IP == "" {
		cfg.IP, err = c.ContainerIP(ctx)
		if err != nil {
			_ = c.Terminate(context.Background())
			return nil, err
		}
	}
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		_ = c.Terminate(context.Background())
		return nil, err
	}
	return &Docker{info: Info{OS: Linux, IP: cfg.IP, WorkDir: "/app/config"}, Container: c, provider: provider}, nil
}

func (d *Docker) Info() Info { return d.info }

func (d *Docker) Run(ctx context.Context, cmd Command) (Result, error) {
	if cmd.Path == "" {
		return Result{}, fmt.Errorf("empty executable")
	}
	cli := d.provider.Client()
	created, err := cli.ExecCreate(ctx, d.Container.GetContainerID(), client.ExecCreateOptions{
		Cmd: append([]string{cmd.Path}, cmd.Args...), WorkingDir: cmd.Dir, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return Result{}, err
	}
	attached, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return Result{}, err
	}
	defer attached.Close()
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()
	var stdout, stderr bytes.Buffer
	// Drain while the process runs, so output larger than Docker's socket buffer
	// cannot deadlock the process before an exit-status inspection.
	_, err = stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
	r := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		return r, err
	}
	for {
		status, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
		if err != nil {
			return r, err
		}
		if !status.Running {
			r.ExitCode = status.ExitCode
			return r.check(nil)
		}
		if err := pause(ctx, 10*time.Millisecond); err != nil {
			return r, err
		}
	}
}

func (d *Docker) WriteFile(ctx context.Context, path string, data []byte) error {
	if _, err := d.Run(ctx, Command{Path: "sh", Args: []string{"-c", `mkdir -p -- "$(dirname -- "$1")"`, "machine", path}}); err != nil {
		return err
	}
	return d.Container.CopyToContainer(ctx, data, path, 0600)
}

func (d *Docker) Start(ctx context.Context, cmd Command) (Process, error) {
	if cmd.Path == "" {
		return nil, fmt.Errorf("empty executable")
	}
	dir := "/tmp/nylon-process-" + uuid.NewString()
	if _, err := d.Run(ctx, Command{Path: "mkdir", Args: []string{"-p", dir}}); err != nil {
		return nil, err
	}
	args := []string{shellQuote(cmd.Path)}
	for _, a := range cmd.Args {
		args = append(args, shellQuote(a))
	}
	script := "#!/bin/sh\n"
	if cmd.Dir != "" {
		script += "cd -- " + shellQuote(cmd.Dir) + " || exit 127\n"
	}
	script += strings.Join(args, " ") + " >" + shellQuote(dir+"/stdout") + " 2>" + shellQuote(dir+"/stderr") + " &\n"
	script += "child=$!\nprintf '%s' \"$child\" >" + shellQuote(dir+"/pid") + "\nwait \"$child\"\ncode=$?\nprintf '%s' \"$code\" >" + shellQuote(dir+"/exit") + "\n"
	if err := d.WriteFile(ctx, dir+"/run.sh", []byte(script)); err != nil {
		return nil, err
	}
	_, err := d.Run(ctx, Command{Path: "sh", Args: []string{"-c", `touch "$1/stdout" "$1/stderr"
sh "$1/run.sh" </dev/null >/dev/null 2>&1 &
n=0
while [ ! -s "$1/pid" ] && [ ! -s "$1/exit" ]; do
  n=$((n+1)); [ "$n" -lt 100 ] || exit 1
  sleep 0.05
done`, "machine", dir}})
	if err != nil {
		return nil, err
	}
	return &dockerProcess{machine: d, dir: dir}, nil
}

func (d *Docker) Close(ctx context.Context) error {
	defer d.provider.Close()
	return d.Container.Terminate(ctx)
}

type dockerProcess struct {
	machine *Docker
	dir     string
}

func (p *dockerProcess) Poll(ctx context.Context) (Result, bool, error) {
	r, err := p.machine.Run(ctx, Command{Path: "sh", Args: []string{"-c", `code=$(cat "$1/exit" 2>/dev/null)
printf '{"stdout":"%s","stderr":"%s","code":%s}' "$(base64 -w0 "$1/stdout")" "$(base64 -w0 "$1/stderr")" "${code:-null}"`, "machine", p.dir}})
	if err != nil {
		return Result{}, false, err
	}
	return processOutput(r.Stdout)
}

func (p *dockerProcess) Stop(ctx context.Context) error {
	_, err := p.machine.Run(ctx, Command{Path: "sh", Args: []string{"-c", `if [ ! -f "$1/exit" ] && [ -f "$1/pid" ]; then kill "$(cat "$1/pid")" 2>/dev/null || true; fi`, "machine", p.dir}})
	return err
}
