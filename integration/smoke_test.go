//go:build smoke

package integration

import (
	"context"
	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"os"
	"path/filepath"
	"testing"
)

func createContainer(ctx context.Context, t *testing.T, command []string, waitFor string) (testcontainers.Container, error) {
	t.Helper()
	nylonPath, err := filepath.Abs(filepath.Join("../", "nylon"))
	require.NoError(t, err)
	r, err := os.Open(nylonPath)
	require.NoError(t, err)

	centralPath, err := filepath.Abs(filepath.Join("fixtures", "testcentral1.yaml"))
	require.NoError(t, err)
	r2, err := os.Open(centralPath)
	require.NoError(t, err)

	nodePath, err := filepath.Abs(filepath.Join("fixtures", "testnode1.yaml"))
	require.NoError(t, err)
	r3, err := os.Open(nodePath)
	require.NoError(t, err)

	return testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "busybox:1.37-glibc",
			HostConfigModifier: func(config *container.HostConfig) {
				config.CapAdd = []string{"NET_ADMIN"}
			},
			Files: []testcontainers.ContainerFile{
				{
					Reader:            r,
					HostFilePath:      nylonPath, // will be discarded internally
					ContainerFilePath: "/nylon",
					FileMode:          0o700,
				},
				{
					Reader:            r2,
					HostFilePath:      centralPath, // will be discarded internally
					ContainerFilePath: "/central.yaml",
					FileMode:          0o700,
				},
				{
					Reader:            r3,
					HostFilePath:      nodePath, // will be discarded internally
					ContainerFilePath: "/node.yaml",
					FileMode:          0o700,
				},
			},
			Cmd:        command,
			WaitingFor: wait.ForLog(waitFor),
		},
		Started: true,
	})
}

func TestNylonExecutes(t *testing.T) {
	ctx := context.Background()
	_, err := createContainer(ctx, t, []string{"/nylon"}, "Nylon is a mesh networking system.")
	require.NoError(t, err)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
}

func TestNylonRuns(t *testing.T) {
	ctx := context.Background()
	// this is very bad
	_, err := createContainer(ctx, t, []string{"sh", "-c", "mkdir /dev/net && mknod /dev/net/tun c 10 200 && /nylon -c /central.yaml -n /node.yaml run"}, "Nylon has been initialized. To gracefully exit, send SIGINT or Ctrl+C.")
	require.NoError(t, err)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}
}
