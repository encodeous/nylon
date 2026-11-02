package machine

import (
	"bytes"
	"context"
	"net/netip"
	"os/exec"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
)

type DockerConfig struct {
	Name    string
	Network string
	IP      string
}

type Docker struct {
	info      Info
	Container testcontainers.Container
}

func NewDocker(ctx context.Context, cfg DockerConfig) (*Docker, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:          "nylon-debug:latest",
			Entrypoint:     []string{"/bin/sh", "-c", "exec sleep infinity"},
			Networks:       []string{cfg.Network},
			NetworkAliases: map[string][]string{cfg.Network: {cfg.Name}},
			HostConfigModifier: func(h *container.HostConfig) {
				h.Privileged = true
				h.CapAdd = []string{"NET_ADMIN"}
			},
			EndpointSettingsModifier: func(endpoints map[string]*network.EndpointSettings) {
				endpoints[cfg.Network].IPAMConfig = &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr(cfg.IP)}
			},
		},
		Started: true,
	})
	if err != nil {
		return nil, err
	}
	return &Docker{info: Info{OS: Linux, IP: cfg.IP, WorkDir: "/app/config"}, Container: c}, nil
}

func (d *Docker) Info() Info { return d.info }

func (d *Docker) Run(ctx context.Context, command Command) (Result, error) {
	args := append([]string{"exec", d.Container.GetContainerID(), command.Path}, command.Args...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	return result.check(err)
}

func (d *Docker) WriteFile(ctx context.Context, path string, data []byte) error {
	if _, err := d.Run(ctx, Command{Path: "sh", Args: []string{"-c", `mkdir -p -- "$(dirname -- "$1")"`, "machine", path}}); err != nil {
		return err
	}
	return d.Container.CopyToContainer(ctx, data, path, 0600)
}

func (d *Docker) Start(ctx context.Context, cmd Command) (Process, error) {
	dir := d.info.WorkDir
	args := []string{shellQuote(cmd.Path)}
	for _, a := range cmd.Args {
		args = append(args, shellQuote(a))
	}
	script := "#!/bin/sh\ncd " + shellQuote(dir) + "\n"
	script += strings.Join(args, " ") + " >" + shellQuote(dir+"/stdout") + " 2>" + shellQuote(dir+"/stderr") + " &\n"
	script += "child=$!\nprintf '%s' \"$child\" >" + shellQuote(dir+"/pid") + "\nwait \"$child\"\ncode=$?\nprintf '%s' \"$code\" >" + shellQuote(dir+"/exit") + "\n"
	if err := d.WriteFile(ctx, dir+"/run.sh", []byte(script)); err != nil {
		return nil, err
	}
	_, err := d.Run(ctx, Command{Path: "sh", Args: []string{"-c", `touch "$1/stdout" "$1/stderr"
sh "$1/run.sh" </dev/null >/dev/null 2>&1 &
`, "machine", dir}})
	if err != nil {
		return nil, err
	}
	return &dockerProcess{machine: d, dir: dir}, nil
}

func (d *Docker) Close(ctx context.Context) error {
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
	_, err := p.machine.Run(ctx, Command{Path: "sh", Args: []string{"-c", `kill "$(cat "$1/pid")"`, "machine", p.dir}})
	return err
}
