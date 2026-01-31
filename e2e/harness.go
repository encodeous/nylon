//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/build"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	ImageName = "nylon-debug:latest"
	AppPort   = "57175/udp"
)

type Harness struct {
	t          *testing.T
	mu         sync.Mutex
	ctx        context.Context
	Network    *testcontainers.DockerNetwork
	Nodes      map[string]testcontainers.Container
	LogBuffers map[string]*LogBuffer
	ImageName  string
	RootDir    string
}
type LogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *LogBuffer) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *LogBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// NewHarness creates a test harness with a specific subnet to avoid collisions
func NewHarness(t *testing.T, subnet, gateway string) *Harness {
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
		LogBuffers: make(map[string]*LogBuffer),
		RootDir:    rootDir,
	}
	h.buildImage()
	t.Cleanup(func() {
		h.Cleanup()
	})
	return h
}

func (h *Harness) buildImage() {
	h.t.Log("Pre-building nylon-debug:latest image...")
	req := testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{
			Context:    h.RootDir,
			Dockerfile: "Dockerfile",
			KeepImage:  true,
			Repo:       "nylon-debug",
			Tag:        "latest",
			BuildOptionsModifier: func(buildOptions *build.ImageBuildOptions) {
				buildOptions.Target = "debug"
			},
		},
	}

	// Creating the container triggers the build
	c, err := testcontainers.GenericContainer(h.ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          false,
	})
	if err != nil {
		h.t.Fatalf("Failed to build image: %v", err)
	}

	// We don't need this container, just the image.
	if err := c.Terminate(h.ctx); err != nil {
		h.t.Logf("Warning: failed to terminate builder container: %v", err)
	}
}

var ansiRegex = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func StripAnsi(s string) string {
	return ansiRegex.ReplaceAllString(s, "")
}

type LogConsumer struct {
	Name   string
	Buffer *LogBuffer
}

func (g *LogConsumer) Accept(l testcontainers.Log) {
	content := string(l.Content)
	// Strip ANSI codes for easier processing and cleaner output
	cleanContent := StripAnsi(content)
	fmt.Printf("[%s] %s", g.Name, cleanContent)
	if g.Buffer != nil {
		g.Buffer.Write([]byte(cleanContent))
	}
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
		WaitingFor: wait.ForLog("Nylon has been initialized").WithStartupTimeout(15 * time.Second),
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
		Name: h.t.Name() + "-" + name,
	}
	container, err := testcontainers.GenericContainer(h.ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		h.t.Fatalf("failed to start container %s: %v", name, err)
	}
	buffer := &LogBuffer{}
	h.mu.Lock()
	h.LogBuffers[name] = buffer
	h.Nodes[name] = container
	h.mu.Unlock()
	container.FollowOutput(&LogConsumer{Name: name, Buffer: buffer})
	container.StartLogProducer(h.ctx)
	return container
}
func (h *Harness) WaitForLog(nodeName string, pattern string) {
	h.mu.Lock()
	buffer, ok := h.LogBuffers[nodeName]
	h.mu.Unlock()
	if !ok {
		h.t.Fatalf("log buffer for node %s not found", nodeName)
	}
	// Poll the buffer
	timeout := time.After(15 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-timeout:
			h.t.Fatalf("timed out waiting for log pattern %q in node %s", pattern, nodeName)
		case <-ticker.C:
			if strings.Contains(buffer.String(), pattern) {
				return
			}
		case <-h.ctx.Done():
			h.t.Fatal("context canceled")
		}
	}
}
func (h *Harness) WaitForMatch(nodeName string, pattern string) {
	h.mu.Lock()
	buffer, ok := h.LogBuffers[nodeName]
	h.mu.Unlock()
	if !ok {
		h.t.Fatalf("log buffer for node %s not found", nodeName)
	}

	// Compile the regex once before the loop
	re, err := regexp.Compile(pattern)
	if err != nil {
		h.t.Fatalf("invalid regex pattern %q: %v", pattern, err)
	}

	// Poll the buffer
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			h.t.Fatalf("timed out waiting for regex match %q in node %s", pattern, nodeName)
		case <-ticker.C:
			// Check against the compiled regex
			if re.MatchString(buffer.String()) {
				return
			}
		case <-h.ctx.Done():
			h.t.Fatal("context canceled")
		}
	}
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
	case <-time.After(15 * time.Second):
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
