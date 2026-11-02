//go:build e2e && linux

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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/joho/godotenv"
	"github.com/klauspost/compress/zstd"
)

type windowsPrerequisites struct{ image, binary, dll string }

var windowsPreflight struct {
	sync.Once
	prepared windowsPrerequisites
	err      error
}

func preflight(ctx context.Context, root string) (windowsPrerequisites, error) {
	windowsPreflight.Do(func() { windowsPreflight.prepared, windowsPreflight.err = prepareWindows(ctx, root) })
	return windowsPreflight.prepared, windowsPreflight.err
}

func prepareWindows(ctx context.Context, root string) (windowsPrerequisites, error) {
	dir := filepath.Join(root, "workdir", "e2e")
	p := windowsPrerequisites{
		image:  filepath.Join(dir, "images", "nylon-ci-win2025.qcow2"),
		binary: filepath.Join(dir, "nylon.exe"),
		dll:    filepath.Join(dir, "artifacts", "wintun-0.14.1-amd64.dll"),
	}
	if err := os.MkdirAll(filepath.Dir(p.image), 0755); err != nil {
		return p, err
	}
	if _, err := os.Stat(p.image); os.IsNotExist(err) {
		_ = godotenv.Load(filepath.Join(root, ".env"))
		if err := downloadWindowsImage(ctx, p.image); err != nil {
			return p, err
		}
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", p.binary, ".")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=windows", "GOARCH=amd64")
	if out, err := cmd.CombinedOutput(); err != nil {
		return p, fmt.Errorf("build Windows Nylon: %w: %s", err, out)
	}
	if _, err := os.Stat(p.dll); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(p.dll), 0755); err != nil {
			return p, err
		}
		resp, err := http.Get("https://www.wintun.net/builds/wintun-0.14.1.zip")
		if err != nil {
			return p, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return p, err
		}
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return p, err
		}
		dll, err := archive.Open("wintun/bin/amd64/wintun.dll")
		if err != nil {
			return p, err
		}
		defer dll.Close()
		data, err = io.ReadAll(dll)
		if err != nil {
			return p, err
		}
		if err := os.WriteFile(p.dll, data, 0644); err != nil {
			return p, err
		}
	}
	return p, nil
}

func downloadWindowsImage(ctx context.Context, path string) error {
	const payloadHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	url := os.Getenv("AWS_ENDPOINT_URL_S3") + "/nylon-ci-windows-image/nylon-ci-win2025.qcow2.zst"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	credentials := aws.Credentials{AccessKeyID: os.Getenv("AWS_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY")}
	if err := v4.NewSigner().SignHTTP(ctx, credentials, req, payloadHash, "s3", "auto", time.Now()); err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Windows image: HTTP %d", resp.StatusCode)
	}
	decoder, err := zstd.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer decoder.Close()
	file, err := os.Create(path + ".partial")
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, decoder); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
