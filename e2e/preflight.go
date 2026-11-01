//go:build e2e

package e2e

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/encodeous/nylon/e2e/machine"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

type windowsPrerequisites struct {
	config      machine.WindowsConfig
	binary, dll string
}

var windowsPreflight struct {
	sync.Once
	prepared windowsPrerequisites
	err      error
}

// preflight completes shared preparation before StartMachines launches goroutines.
// Linux scenarios need no Windows artifacts. Explicit Windows NodeSpec overrides
// take this path too, regardless of the default placement.
func (h *Harness) preflight(needsWindows bool) (windowsPrerequisites, error) {
	if !needsWindows {
		return windowsPrerequisites{}, nil
	}
	windowsPreflight.Do(func() {
		fmt.Println("Preflight: preparing Windows base image and Nylon artifacts...")
		cfg, err := machine.WindowsConfigFromEnv(h.RootDir)
		if err == nil {
			cfg.BaseImage, err = machine.PrepareWindowsImage(h.ctx, cfg)
		}
		prepared := windowsPrerequisites{config: cfg}
		if err == nil {
			prepared.binary, prepared.dll, err = prepareWindowsArtifacts(h.ctx, h.RootDir)
		}
		windowsPreflight.prepared, windowsPreflight.err = prepared, err
	})
	if windowsPreflight.err != nil {
		return windowsPrerequisites{}, windowsPreflight.err
	}
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return windowsPrerequisites{}, err
	}
	defer provider.Close()
	inspection, err := provider.Client().NetworkInspect(h.ctx, h.Network.ID, client.NetworkInspectOptions{})
	if err != nil {
		return windowsPrerequisites{}, err
	}
	prepared := windowsPreflight.prepared
	prepared.config.Subnet, prepared.config.HostHelperImage = h.Subnet, ImageName
	prepared.config.Bridge = inspection.Network.Options["com.docker.network.bridge.name"]
	if prepared.config.Bridge == "" {
		prepared.config.Bridge = "br-" + inspection.Network.ID[:12]
	}
	return prepared, nil
}

func prepareWindowsArtifacts(ctx context.Context, root string) (string, string, error) {
	dir := filepath.Join(root, "workdir", "e2e", "artifacts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	binary := os.Getenv("NYLON_E2E_WINDOWS_BINARY")
	if binary != "" && !filepath.IsAbs(binary) {
		binary = filepath.Join(root, binary)
	}
	if binary == "" {
		binary = filepath.Join(dir, "nylon.exe")
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("build Windows Nylon: %w: %s", err, out)
		}
	}
	if _, err := os.Stat(binary); err != nil {
		return "", "", err
	}
	dll := os.Getenv("NYLON_E2E_WINTUN_DLL")
	if dll != "" {
		if !filepath.IsAbs(dll) {
			dll = filepath.Join(root, dll)
		}
		if _, err := os.Stat(dll); err != nil {
			return "", "", err
		}
		return binary, dll, nil
	}
	// Match the version and architecture already used by the release workflow.
	dll = filepath.Join(dir, "wintun-0.14.1-amd64.dll")
	if _, err := os.Stat(dll); err == nil {
		return binary, dll, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.wintun.net/builds/wintun-0.14.1.zip", nil)
	if err != nil {
		return "", "", err
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download Wintun: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", "", err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "", err
	}
	for _, entry := range archive.File {
		if entry.Name != "wintun/bin/amd64/wintun.dll" {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return "", "", err
		}
		defer reader.Close()
		data, err := io.ReadAll(io.LimitReader(reader, 16<<20))
		if err != nil {
			return "", "", err
		}
		tmp, err := os.CreateTemp(dir, ".wintun-*.dll")
		if err != nil {
			return "", "", err
		}
		defer os.Remove(tmp.Name())
		_, writeErr := tmp.Write(data)
		closeErr := tmp.Close()
		if writeErr != nil {
			return "", "", writeErr
		}
		if closeErr != nil {
			return "", "", closeErr
		}
		if err := os.Rename(tmp.Name(), dll); err != nil {
			return "", "", err
		}
		return binary, dll, nil
	}
	return "", "", fmt.Errorf("Wintun archive has no amd64 DLL")
}
