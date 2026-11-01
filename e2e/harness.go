//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/encodeous/nylon/e2e/machine"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/encoding/protojson"
)

var (
	ImageName   = "nylon-debug:latest"
	AppPort     = "57175/udp"
	WaitTimeout = 2 * time.Minute
	networkMu   sync.Mutex
)

type Harness struct {
	t          *testing.T
	mu         sync.Mutex
	ctx        context.Context
	Network    *testcontainers.DockerNetwork
	Nodes      map[string]testcontainers.Container
	Machines   map[string]machine.Machine
	processes  map[string]machine.Process
	logCancel  context.CancelFunc
	logWG      sync.WaitGroup
	LogManager *LogManager
	ImageName  string
	RootDir    string
	Subnet     string
	Gateway    string
	testDir    string
}

// NewHarness creates a test harness with a unique subnet
func NewHarness(t *testing.T) *Harness {
	ctx := context.Background()
	// Find root directory (assuming we are in e2e/<test_name>)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Traversing up to find go.mod
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			t.Fatal("could not find project root")
		}
		rootDir = parent
	}

	newNetwork, subnet, gateway, err := createNetwork(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Allocated subnet: %s, gateway: %s", subnet, gateway)

	ctx, logCancel := context.WithCancel(ctx)
	h := &Harness{
		t:          t,
		ctx:        ctx,
		Network:    newNetwork,
		Nodes:      make(map[string]testcontainers.Container),
		Machines:   make(map[string]machine.Machine),
		processes:  make(map[string]machine.Process),
		logCancel:  logCancel,
		LogManager: NewLogManager(),
		RootDir:    rootDir,
		Subnet:     subnet,
		Gateway:    gateway,
	}
	// Image building is handled in MainTest
	t.Cleanup(func() {
		h.Cleanup()
	})
	return h
}

// createNetwork creates a bridge network on a free subnet. networkMu only
// serializes allocation within this process, so another process (another test
// package or CI job on the same daemon) can still claim the subnet between
// listing networks and creating ours. Docker rejects that with an overlap
// error, so we allocate again and retry.
func createNetwork(ctx context.Context) (*testcontainers.DockerNetwork, string, string, error) {
	networkMu.Lock()
	defer networkMu.Unlock()

	var lastErr error
	for range 5 {
		subnet, gateway, err := AllocateDockerSubnet(ctx)
		if err != nil {
			return nil, "", "", err
		}
		// Create network with a Docker-checked non-overlapping explicit subnet.
		nw, err := tcnetwork.New(ctx,
			tcnetwork.WithAttachable(),
			tcnetwork.WithDriver("bridge"),
			tcnetwork.WithIPAM(&network.IPAM{
				Driver: "default",
				Config: []network.IPAMConfig{
					{
						Subnet: netip.MustParsePrefix(subnet),
						// Dynamic Docker addresses stay clear of static VM addresses.
						IPRange: netip.PrefixFrom(netip.MustParseAddr(GetIP(subnet, 128)), 25),
						Gateway: netip.MustParseAddr(gateway),
					},
				},
			}))
		if err == nil {
			return nw, subnet, gateway, nil
		}
		if !strings.Contains(err.Error(), "overlaps") {
			return nil, "", "", err
		}
		lastErr = err
	}
	return nil, "", "", fmt.Errorf("could not allocate a free subnet: %w", lastErr)
}

type NodeSpec struct {
	Name              string
	IP                string
	CentralConfigPath string
	NodeConfigPath    string
	ExtraArgs         []string
	// OS is used by StartMachines. Empty selects NYLON_E2E_MACHINE (linux by default).
	OS machine.OS
}

func (h *Harness) StartNodes(specs ...NodeSpec) {
	h.t.Helper()
	// t.Fatal must only be called from the test goroutine, so the workers
	// collect errors and we fail here once all of them are done.
	errs := make([]error, len(specs))
	var wg sync.WaitGroup
	for i, spec := range specs {
		wg.Go(func() {
			_, errs[i] = h.startNode(spec.Name, spec.IP, spec.CentralConfigPath, spec.NodeConfigPath, spec.ExtraArgs...)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		h.t.Fatal(err)
	}
}
func (h *Harness) StartNode(name string, ip string, centralConfigPath, nodeConfigPath string, extraArgs ...string) testcontainers.Container {
	h.t.Helper()
	cont, err := h.startNode(name, ip, centralConfigPath, nodeConfigPath, extraArgs...)
	if err != nil {
		h.t.Fatal(err)
	}
	return cont
}

// containerName builds a Docker-safe container name. Subtest names contain
// '/', which Docker does not allow in names.
func (h *Harness) containerName(name string) string {
	return strings.ReplaceAll(h.t.Name(), "/", "_") + "-" + name
}

func (h *Harness) startNode(name string, ip string, centralConfigPath, nodeConfigPath string, extraArgs ...string) (testcontainers.Container, error) {
	h.t.Logf("Starting node %s at %s", name, ip)
	req := testcontainers.ContainerRequest{
		Image:    ImageName,
		Networks: []string{h.Network.Name},
		NetworkAliases: map[string][]string{
			h.Network.Name: {name},
		},
		Files: []testcontainers.ContainerFile{
			{
				HostFilePath:      centralConfigPath,
				ContainerFilePath: "/app/config/central.yaml",
				FileMode:          0644,
			},
			{
				HostFilePath:      nodeConfigPath,
				ContainerFilePath: "/app/config/node.yaml",
				FileMode:          0644,
			},
		},
		Cmd: extraArgs,
		Env: map[string]string{
			"NYLON_LOG_LEVEL": "debug",
		},
		WaitingFor: wait.ForLog("Nylon has been initialized").WithStartupTimeout(30 * time.Second),
		HostConfigModifier: func(hostConfig *container.HostConfig) {
			hostConfig.Privileged = true
			hostConfig.CapAdd = []string{"NET_ADMIN"}
		},
		EndpointSettingsModifier: func(m map[string]*network.EndpointSettings) {
			if ip != "" {
				if s, ok := m[h.Network.Name]; ok {
					s.IPAMConfig = &network.EndpointIPAMConfig{
						IPv4Address: netip.MustParseAddr(ip),
					}
				}
			}
		},
		LogConsumerCfg: &testcontainers.LogConsumerConfig{
			Consumers: []testcontainers.LogConsumer{
				&UnifiedLogConsumer{Node: name, Manager: h.LogManager},
			},
		},
		Name: h.containerName(name),
	}
	cont, err := testcontainers.GenericContainer(h.ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	// GenericContainer can return a container alongside an error (e.g. when
	// the wait strategy times out), so track it for cleanup either way.
	if cont != nil {
		h.mu.Lock()
		h.Nodes[name] = cont
		h.mu.Unlock()
	}
	if err != nil {
		return cont, fmt.Errorf("failed to start container %s: %w", name, err)
	}
	return cont, nil
}

func (h *Harness) WaitForLog(nodeName string, pattern string) {
	h.waitFor(nodeName, SourceStdout, pattern, false)
}
func (h *Harness) WaitForMatch(nodeName string, pattern string) {
	h.waitFor(nodeName, SourceStdout, pattern, true)
}
func (h *Harness) WaitForStatus(t *testing.T, nodeName string, check func(*protocol.StatusResponse) bool) {
	t.Helper()
	start := time.Now()
	for {
		if time.Since(start) > WaitTimeout {
			stdout, _, _ := h.Exec(nodeName, []string{"nylon", "status", "-i", "nylon0", "--json"})
			h.t.Fatalf("timed out waiting for status predicate in node %s. Current status:\n%s", nodeName, stdout)
		}
		status, err := h.ReadStatus(nodeName)
		if err == nil && check(status) {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func (h *Harness) WaitForTCPListener(t *testing.T, nodeName string, port int) {
	t.Helper()
	deadline := time.Now().Add(WaitTimeout)
	var stdout, stderr string
	var err error
	for {
		stdout, stderr, err = h.Exec(nodeName, []string{"ss", "-H", "-ltn", "sport", "=", fmt.Sprintf(":%d", port)})
		if err == nil && stdout != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for TCP listener on node %s port %d: %v\nStdout: %s\nStderr: %s", nodeName, port, err, stdout, stderr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *Harness) ReadStatus(nodeName string) (*protocol.StatusResponse, error) {
	stdout, _, err := h.Exec(nodeName, []string{"nylon", "status", "-i", "nylon0", "--json"})
	if err != nil {
		return nil, err
	}
	resp := &protocol.IpcResponse{}
	if err := protojson.Unmarshal([]byte(stdout), resp); err != nil {
		return nil, err
	}
	return resp.GetStatus(), nil
}

func HasSelectedRoute(status *protocol.StatusResponse, prefix, nh, router string) bool {
	for _, route := range status.GetRoutes().GetSelected() {
		source := route.GetPubRoute().GetSource()
		if source.GetPrefix() == prefix && route.GetNh() == nh && source.GetNodeId() == router {
			return true
		}
	}
	return false
}

func HasResolvedEndpoint(status *protocol.StatusResponse, address, resolved string) bool {
	for _, neigh := range status.GetNeighbours() {
		for _, ep := range neigh.GetEndpoints() {
			if ep.GetAddress() == address && ep.GetResolved() == resolved {
				return true
			}
		}
	}
	return false
}
func (h *Harness) WaitForTrace(nodeName string, pattern string) {
	h.waitFor(nodeName, SourceTrace, pattern, false)
}
func (h *Harness) waitFor(nodeName string, source LogSource, pattern string, isRegex bool) {
	sub, err := h.LogManager.Subscribe(nodeName, source, pattern, isRegex)
	if err != nil {
		h.t.Fatalf("failed to subscribe: %v", err)
	}
	defer h.LogManager.Unsubscribe(sub)

	select {
	case <-sub.MatchCh:
		return
	case <-time.After(WaitTimeout):
		h.t.Fatalf("timed out waiting for %s pattern %q in node %s", source, pattern, nodeName)
	case <-h.ctx.Done():
		h.t.Fatal("context canceled")
	}
}

// managerWriter feeds an exec stream into the LogManager one line at a time.
// The stream arrives in arbitrary chunks, so a partial line is held until its
// newline arrives; otherwise a pattern split across two chunks never matches.
type managerWriter struct {
	node    string
	source  LogSource
	manager *LogManager
	partial []byte
}

func (w *managerWriter) Write(p []byte) (n int, err error) {
	w.partial = append(w.partial, p...)
	for {
		idx := bytes.IndexByte(w.partial, '\n')
		if idx < 0 {
			break
		}
		w.emit(string(w.partial[:idx+1]))
		w.partial = w.partial[idx+1:]
	}
	return len(p), nil
}

// Flush emits any trailing output that did not end in a newline.
func (w *managerWriter) Flush() {
	if len(w.partial) > 0 {
		w.emit(string(w.partial) + "\n")
		w.partial = nil
	}
}

func (w *managerWriter) emit(line string) {
	content := StripAnsi(line)
	ts := time.Since(w.manager.start).Truncate(time.Millisecond)
	fmt.Printf("[+%s][%s:%s] %s", ts, w.node, w.source, content)
	w.manager.Accept(w.node, w.source, content)
}

func (h *Harness) StartTrace(nodeName string) {
	h.mu.Lock()
	cont, ok := h.Nodes[nodeName]
	h.mu.Unlock()

	if !ok {
		h.t.Fatalf("node %s not found", nodeName)
	}

	h.t.Logf("Started trace for %s", nodeName)

	go func() {
		time.Sleep(time.Second)
		execOptions := client.ExecCreateOptions{
			Cmd:          []string{"nylon", "trace", "-i", "nylon0"},
			AttachStdout: true,
			AttachStderr: true,
			TTY:          false,
		}

		// container.Exec only returns output once the command exits, so the
		// streaming exec goes through our own client. NewDockerClientWithOpts
		// resolves the same daemon that testcontainers uses.
		cli, err := testcontainers.NewDockerClientWithOpts(h.ctx)
		if err != nil {
			// t.Fatal is not allowed outside the test goroutine.
			h.t.Errorf("Failed to start trace for %s: %v", nodeName, err)
			return
		}
		defer cli.Close()

		execIDResp, err := cli.ExecCreate(h.ctx, cont.GetContainerID(), execOptions)
		if err != nil {
			h.t.Errorf("Failed to start trace for %s: %v", nodeName, err)
			return
		}

		resp, err := cli.ExecAttach(h.ctx, execIDResp.ID, client.ExecAttachOptions{})
		if err != nil {
			h.t.Errorf("Failed to start trace for %s: %v", nodeName, err)
			return
		}
		defer resp.Close()

		stdoutWriter := &managerWriter{node: nodeName, source: SourceTrace, manager: h.LogManager}
		_, _ = stdcopy.StdCopy(stdoutWriter, stdoutWriter, resp.Reader)
		stdoutWriter.Flush()
	}()
}
func (h *Harness) Cleanup() {
	h.logCancel()
	h.logWG.Wait()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.processes) != 0 {
		dir := filepath.Join(h.RootDir, "workdir", "e2e", "logs", h.t.Name())
		if err := os.MkdirAll(dir, 0700); err != nil {
			h.t.Logf("create machine log directory: %v", err)
		} else {
			for name, p := range h.processes {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				r, _, err := p.Poll(cleanupCtx)
				cancel()
				if err != nil {
					h.t.Logf("save machine logs for %s: %v", name, err)
					continue
				}
				for suffix, content := range map[string]string{"stdout": r.Stdout, "stderr": r.Stderr} {
					if err := os.WriteFile(filepath.Join(dir, name+"."+suffix+".log"), []byte(content), 0600); err != nil {
						h.t.Logf("save machine log: %v", err)
					}
				}
			}
		}
	}
	for name, m := range h.Machines {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := m.Close(cleanupCtx)
		cancel()
		if err != nil {
			h.t.Logf("failed to close machine %s: %v", name, err)
		}
	}
	for name, c := range h.Nodes {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		err := c.Terminate(cleanupCtx)
		cancel()
		if err != nil {
			h.t.Logf("failed to terminate container %s: %v", name, err)
		}
	}
	if err := h.Network.Remove(context.Background()); err != nil {
		h.t.Logf("failed to remove network: %v", err)
	}
}
func (h *Harness) Exec(nodeName string, cmd []string) (string, string, error) {
	h.mu.Lock()
	m := h.Machines[nodeName]
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()
	if m != nil {
		if len(cmd) == 0 {
			return "", "", fmt.Errorf("empty command")
		}
		path := cmd[0]
		if path == "nylon" && m.Info().OS == machine.Windows {
			path = m.Info().WorkDir + `\nylon.exe`
		}
		r, err := m.Run(h.ctx, machine.Command{Path: path, Args: cmd[1:]})
		return StripAnsi(r.Stdout), StripAnsi(r.Stderr), err
	}

	if !ok {
		return "", "", fmt.Errorf("node %s not found", nodeName)
	}

	code, r, err := container.Exec(h.ctx, cmd)
	if err != nil {
		return "", "", err
	}

	stdoutBuf := new(bytes.Buffer)
	stderrBuf := new(bytes.Buffer)

	// Demultiplex the stream using stdcopy
	_, err = stdcopy.StdCopy(stdoutBuf, stderrBuf, r)
	if err != nil {
		return "", "", fmt.Errorf("failed to copy output: %w", err)
	}

	stdout := StripAnsi(stdoutBuf.String())
	stderr := StripAnsi(stderrBuf.String())

	if code != 0 {
		return stdout, stderr, fmt.Errorf("command exited with code %d: %s\nStderr: %s", code, stdout, stderr)
	}

	return stdout, stderr, nil
}

type BackgroundExec struct {
	Stdout string
	Stderr string
	Err    error
	done   chan struct{}
}

func (e *BackgroundExec) Wait() (string, string, error) {
	select {
	case <-e.done:
		break
	case <-time.After(WaitTimeout):
		return "", "", fmt.Errorf("timed out waiting for command to finish")
	}
	return e.Stdout, e.Stderr, e.Err
}

func (h *Harness) ExecBackground(nodeName string, cmd []string) *BackgroundExec {
	bg := &BackgroundExec{
		done: make(chan struct{}),
	}
	go func() {
		defer close(bg.done)
		bg.Stdout, bg.Stderr, bg.Err = h.Exec(nodeName, cmd)
	}()
	return bg
}

// GetIP returns the IP address of the node in the test network
func (h *Harness) GetIP(nodeName string) (string, error) {
	h.mu.Lock()
	m := h.Machines[nodeName]
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()
	if m != nil {
		return m.Info().IP, nil
	}
	if !ok {
		return "", fmt.Errorf("node %s not found", nodeName)
	}
	return container.ContainerIP(h.ctx)
}
func (h *Harness) PrintLogs(nodeName string) {
	h.mu.Lock()
	p := h.processes[nodeName]
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()
	if p != nil {
		r, _, err := p.Poll(h.ctx)
		h.t.Logf("Logs for %s (error: %v):\n%s\n%s", nodeName, err, r.Stdout, r.Stderr)
		return
	}
	if !ok {
		h.t.Logf("node %s not found for logging", nodeName)
		return
	}
	r, err := container.Logs(h.ctx)
	if err != nil {
		h.t.Logf("failed to get logs for %s: %v", nodeName, err)
		return
	}
	buf := new(bytes.Buffer)
	io.Copy(buf, r)
	h.t.Logf("Logs for %s:\n%s", nodeName, buf.String())
}

func (h *Harness) CopyFile(nodeName string, hostPath string, containerPath string) {
	h.mu.Lock()
	c, ok := h.Nodes[nodeName]
	h.mu.Unlock()
	if !ok {
		h.t.Fatalf("node %s not found", nodeName)
	}
	err := c.CopyFileToContainer(h.ctx, hostPath, containerPath, 0644)
	if err != nil {
		h.t.Fatalf("failed to copy file to container %s: %v", nodeName, err)
	}
}

func (h *Harness) StartDNS(name string, ip string, corefile string, zones map[string]string) testcontainers.Container {
	h.t.Logf("Starting DNS server %s at %s", name, ip)

	tempDir := h.SetupTestDir()
	dnsDir := filepath.Join(tempDir, "dns")
	os.MkdirAll(dnsDir, 0755)

	corefilePath := filepath.Join(dnsDir, "Corefile")
	os.WriteFile(corefilePath, []byte(corefile), 0644)

	files := []testcontainers.ContainerFile{
		{
			HostFilePath:      corefilePath,
			ContainerFilePath: "/etc/coredns/Corefile",
			FileMode:          0644,
		},
	}

	for zoneName, zoneContent := range zones {
		zonePath := filepath.Join(dnsDir, zoneName)
		os.WriteFile(zonePath, []byte(zoneContent), 0644)
		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      zonePath,
			ContainerFilePath: "/etc/coredns/" + zoneName,
			FileMode:          0644,
		})
	}

	req := testcontainers.ContainerRequest{
		Image:    "coredns/coredns:latest",
		Networks: []string{h.Network.Name},
		NetworkAliases: map[string][]string{
			h.Network.Name: {name},
		},
		Cmd:        []string{"-conf", "/etc/coredns/Corefile"},
		Files:      files,
		WaitingFor: wait.ForListeningPort("53/udp"),
		EndpointSettingsModifier: func(m map[string]*network.EndpointSettings) {
			if ip != "" {
				if s, ok := m[h.Network.Name]; ok {
					s.IPAMConfig = &network.EndpointIPAMConfig{
						IPv4Address: netip.MustParseAddr(ip),
					}
				}
			}
		},
		Name: h.containerName(name),
	}

	container, err := testcontainers.GenericContainer(h.ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		h.t.Fatalf("failed to start coredns container %s: %v", name, err)
	}

	h.mu.Lock()
	h.Nodes[name] = container
	h.mu.Unlock()

	return container
}

// SetupTestDir returns the directory for the current test run. The first call
// clears out files left by a previous run; later calls return the same
// directory without wiping it, so helpers such as StartDNS can share it with
// the test.
func (h *Harness) SetupTestDir() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.testDir != "" {
		return h.testDir
	}
	dir := filepath.Join(h.RootDir, "e2e", "runs", h.t.Name())
	// Clean up previous run
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		h.t.Fatal(err)
	}
	h.testDir = dir
	return dir
}

// WriteConfig marshals the config to YAML and writes it to the specified directory with the given filename
func (h *Harness) WriteConfig(dir, filename string, cfg any) string {
	path := filepath.Join(dir, filename)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

// SimpleRouter creates a basic RouterCfg with the given parameters
func SimpleRouter(id string, pubKey state.NyPublicKey, nylonIP string, endpointIP string) state.RouterCfg {
	cfg := state.RouterCfg{
		NodeCfg: state.NodeCfg{
			Id:     state.NodeId(id),
			PubKey: pubKey,
			Addresses: []netip.Addr{
				netip.MustParseAddr(nylonIP),
			},
		},
	}
	if endpointIP != "" {
		cfg.Endpoints = []string{
			fmt.Sprintf("%s:57175", endpointIP),
		}
	}
	return cfg
}

// SimpleLocal creates a basic LocalCfg with the given parameters and defaults
func SimpleLocal(id string, key state.NyPrivateKey) state.LocalCfg {
	return state.LocalCfg{
		Id:             state.NodeId(id),
		Key:            key,
		Port:           57175,
		NoNetConfigure: false,
		InterfaceName:  "nylon0",
	}
}
