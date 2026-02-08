//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	ImageName   = "nylon-debug:latest"
	AppPort     = "57175/udp"
	WaitTimeout = 2 * time.Minute
)

type Harness struct {
	t          *testing.T
	mu         sync.Mutex
	ctx        context.Context
	Network    *testcontainers.DockerNetwork
	Nodes      map[string]testcontainers.Container
	LogManager *LogManager
	ImageName  string
	RootDir    string
	Subnet     string
	Gateway    string
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

	subnet, gateway := GlobalNetworkAllocator.Allocate()
	t.Logf("Allocated subnet: %s, gateway: %s", subnet, gateway)

	// Create network with specific subnet
	newNetwork, err := tcnetwork.New(ctx,
		tcnetwork.WithAttachable(),
		tcnetwork.WithDriver("bridge"),
		tcnetwork.WithIPAM(&network.IPAM{
			Driver: "default",
			Config: []network.IPAMConfig{
				{
					Subnet:  subnet,
					Gateway: gateway,
				},
			},
		}))
	if err != nil {
		t.Fatal(err)
	}
	h := &Harness{
		t:          t,
		ctx:        ctx,
		Network:    newNetwork,
		Nodes:      make(map[string]testcontainers.Container),
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

type NodeSpec struct {
	Name              string
	IP                string
	CentralConfigPath string
	NodeConfigPath    string
}

func (h *Harness) StartNodes(specs ...NodeSpec) {
	var wg sync.WaitGroup
	wg.Add(len(specs))
	for _, spec := range specs {
		go func(s NodeSpec) {
			defer wg.Done()
			h.StartNode(s.Name, s.IP, s.CentralConfigPath, s.NodeConfigPath)
		}(spec)
	}
	wg.Wait()
}
func (h *Harness) StartNode(name string, ip string, centralConfigPath, nodeConfigPath string) testcontainers.Container {
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
		Cmd: nil, // Entrypoint already handles "run -v"
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
						IPv4Address: ip,
					}
				}
			}
		},
		LogConsumerCfg: &testcontainers.LogConsumerConfig{
			Consumers: []testcontainers.LogConsumer{
				&UnifiedLogConsumer{Node: name, Manager: h.LogManager},
			},
		},
		Name: h.t.Name() + "-" + name,
	}
	cont, err := testcontainers.GenericContainer(h.ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		h.t.Fatalf("failed to start container %s: %v", name, err)
	}
	h.mu.Lock()
	h.Nodes[name] = cont
	h.mu.Unlock()
	return cont
}

func (h *Harness) WaitForLog(nodeName string, pattern string) {
	h.waitFor(nodeName, SourceStdout, pattern, false)
}
func (h *Harness) WaitForMatch(nodeName string, pattern string) {
	h.waitFor(nodeName, SourceStdout, pattern, true)
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

type managerWriter struct {
	node    string
	source  LogSource
	manager *LogManager
}

func (w *managerWriter) Write(p []byte) (n int, err error) {
	w.manager.Accept(w.node, w.source, string(p))
	return len(p), nil
}

func (h *Harness) StartTrace(nodeName string) {
	h.mu.Lock()
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()

	if !ok {
		h.t.Fatalf("node %s not found", nodeName)
	}

	go func() {
		_, r, err := container.Exec(h.ctx, []string{"nylon", "inspect", "nylon0", "--trace"})
		if err != nil {
			return
		}
		stdoutWriter := &managerWriter{node: nodeName, source: SourceTrace, manager: h.LogManager}
		stdcopy.StdCopy(stdoutWriter, stdoutWriter, r)
	}()
}
func (h *Harness) Cleanup() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, c := range h.Nodes {
		if err := c.Terminate(h.ctx); err != nil {
			h.t.Logf("failed to terminate container %s: %v", name, err)
		}
	}
	if err := h.Network.Remove(context.Background()); err != nil {
		h.t.Logf("failed to remove network: %v", err)
	}
}
func (h *Harness) Exec(nodeName string, cmd []string) (string, string, error) {
	h.mu.Lock()
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()

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
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("node %s not found", nodeName)
	}
	return container.ContainerIP(h.ctx)
}
func (h *Harness) PrintLogs(nodeName string) {
	h.mu.Lock()
	container, ok := h.Nodes[nodeName]
	h.mu.Unlock()
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
						IPv4Address: ip,
					}
				}
			}
		},
		Name: h.t.Name() + "-" + name,
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

// SetupTestDir creates a directory for the current test run
func (h *Harness) SetupTestDir() string {
	dir := filepath.Join(h.RootDir, "e2e", "runs", h.t.Name())
	// Clean up previous run
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		h.t.Fatal(err)
	}
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
		cfg.Endpoints = []*state.DynamicEndpoint{
			state.NewDynamicEndpoint(fmt.Sprintf("%s:57175", endpointIP)),
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
