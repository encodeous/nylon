//go:build linux

package machine

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWinRMHonorsCanceledContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.(http.Flusher).Flush()
		close(started)
		<-release
	}))
	defer server.Close()
	_, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	w := QEMUWindows{port: port, cfg: WindowsConfig{Username: "test", Password: "test"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := w.runPS(ctx, "'hello'", ""); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		close(release)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled operation, got %v", err)
		}
	case <-time.After(time.Second):
		t.Errorf("runPS still blocked after context expired: ctx.Err()=%v", ctx.Err())
		close(release)
		t.Logf("after HTTP response body was released: %v", <-done)
	}
}

func TestCloseCanRetryAfterCancellation(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.qcow2")
	vars := filepath.Join(dir, "uefi-vars.fd")
	for _, p := range []string{disk, vars} {
		if err := os.WriteFile(p, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	done := make(chan struct{})
	w := QEMUWindows{dir: dir, cmd: cmd, done: done}
	// Model an already cancelled harness budget and a reaper not scheduled yet.
	go func() { _ = cmd.Wait(); close(reaped); <-done }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first := w.Close(ctx)
	<-reaped
	close(done)
	second := w.Close(context.Background())
	if second != nil {
		t.Fatalf("cleanup retry: %v (first: %v)", second, first)
	}
	for _, path := range []string{disk, vars} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("cleanup did not remove %s: %v", path, err)
		}
	}
}
