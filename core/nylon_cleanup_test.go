package core

import (
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCleanupRunsDownCommandsOnlyAfterStartupConfiguredHost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses touch")
	}
	dir := t.TempDir()
	preDown, postDown := filepath.Join(dir, "pre-down"), filepath.Join(dir, "post-down")
	n := &Nylon{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	n.PreDown = []string{"touch " + preDown}
	n.PostDown = []string{"touch " + postDown}
	// A failed start, such as an interface already owned by another instance,
	// must not run down commands that would tear down that instance.
	require.NoError(t, n.cleanupTransport())
	require.NoFileExists(t, preDown)
	require.NoFileExists(t, postDown)
	n.systemConfigured = true
	require.NoError(t, n.cleanupTransport())
	require.FileExists(t, preDown)
	require.FileExists(t, postDown)
}
