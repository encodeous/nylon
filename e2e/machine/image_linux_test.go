package machine

import (
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func smallBase(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("qemu-img"); err != nil {
		t.Skip("qemu-img is not installed")
	}
	path := filepath.Join(t.TempDir(), "base.qcow2")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", path, "8M").CombinedOutput(); err != nil {
		t.Fatalf("create base: %v: %s", err, out)
	}
	return path
}

func TestOverlaysIsolateWritesAndPreserveBase(t *testing.T) {
	base := smallBase(t)
	if _, err := exec.LookPath("qemu-io"); err != nil {
		t.Skip("qemu-io is not installed")
	}
	before, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	baseHash := sha256.Sum256(before)
	paths := []string{filepath.Join(t.TempDir(), "first.qcow2"), filepath.Join(t.TempDir(), "second.qcow2")}
	for _, path := range paths {
		if err := CreateOverlay(t.Context(), base, path); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("qemu-img", "info", "--output=json", path).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		var info struct {
			Backing string `json:"full-backing-filename"`
			Format  string `json:"backing-filename-format"`
		}
		if err := json.Unmarshal(out, &info); err != nil {
			t.Fatal(err)
		}
		if info.Backing != base || info.Format != "qcow2" {
			t.Fatalf("unexpected backing: %+v", info)
		}
	}
	if out, err := exec.Command("qemu-io", "-f", "qcow2", "-c", "write -P 0x42 0 512", paths[0]).CombinedOutput(); err != nil {
		t.Fatalf("write overlay: %v: %s", err, out)
	}
	if out, err := exec.Command("qemu-io", "-r", "-f", "qcow2", "-c", "read -P 0 0 512", paths[1]).CombinedOutput(); err != nil {
		t.Fatalf("second overlay changed: %v: %s", err, out)
	}
	after, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(after) != baseHash {
		t.Fatal("backing image was modified")
	}
}

func TestR2CacheRejectsIncompleteDownloadAndReusesImage(t *testing.T) {
	base := smallBase(t)
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	compressed := encoder.EncodeAll(data, nil)
	var gets atomic.Int32
	var corrupt atomic.Bool
	corrupt.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") || r.Header.Get("X-Amz-Content-Sha256") == "" {
			t.Error("unsigned R2 request")
			w.WriteHeader(403)
			return
		}
		if r.URL.Path != "/bucket/image.qcow2.zst" {
			t.Errorf("unexpected object: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		gets.Add(1)
		if corrupt.Load() {
			_, _ = w.Write([]byte("interrupted image"))
			return
		}
		_, _ = w.Write(compressed)
	}))
	defer server.Close()
	cfg := WindowsConfig{R2Endpoint: server.URL, R2AccessKey: "test-key", R2SecretKey: "test-secret", Bucket: "bucket", Object: "image.qcow2.zst", CacheDir: t.TempDir()}
	if _, err := PrepareWindowsImage(t.Context(), cfg); err == nil {
		t.Fatal("corrupt compressed image accepted")
	}
	files, err := filepath.Glob(filepath.Join(cfg.CacheDir, "*.qcow2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("incomplete image published to cache")
	}
	corrupt.Store(false)
	path, err := PrepareWindowsImage(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Warm-cache preflight needs no R2 credentials or network request.
	cfg.R2Endpoint, cfg.R2AccessKey, cfg.R2SecretKey = "", "", ""
	cached, err := PrepareWindowsImage(t.Context(), cfg)
	if err != nil || cached != path {
		t.Fatalf("cache reuse: %s, %v", cached, err)
	}
	if gets.Load() != 2 {
		t.Fatalf("got %d downloads; expected one failed and one successful", gets.Load())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0222 != 0 {
		t.Fatal("cached backing image is writable")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(got) != sha256.Sum256(data) {
		t.Fatal("cached image content changed")
	}
}
