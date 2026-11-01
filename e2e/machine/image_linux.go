package machine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/klauspost/compress/zstd"
)

// PrepareWindowsImage runs during preflight, before any VMs are started.
// A completed image is reused until the cache is explicitly cleared.
func PrepareWindowsImage(ctx context.Context, cfg WindowsConfig) (string, error) {
	if cfg.BaseImage != "" {
		path, err := filepath.Abs(cfg.BaseImage)
		if err != nil {
			return "", err
		}
		return path, validateBaseImage(ctx, path)
	}
	cacheDir, err := filepath.Abs(cfg.CacheDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(cacheDir, strings.TrimSuffix(filepath.Base(cfg.Object), ".zst"))
	if _, err := os.Stat(path); err == nil {
		return path, validateBaseImage(ctx, path)
	}
	if cfg.R2Endpoint == "" || cfg.R2AccessKey == "" || cfg.R2SecretKey == "" {
		return "", fmt.Errorf("set AWS_ENDPOINT_URL_S3, AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or NYLON_E2E_WINDOWS_IMAGE")
	}
	u, err := url.Parse(cfg.R2Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", fmt.Errorf("invalid R2 endpoint")
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return "", err
	}
	objectURL := u.JoinPath(cfg.Bucket, cfg.Object).String()
	client := &http.Client{Timeout: 30 * time.Minute}
	response, err := r2Request(ctx, client, cfg, objectURL)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	decoder, err := zstd.NewReader(response.Body)
	if err != nil {
		return "", err
	}
	defer decoder.Close()
	tmp, err := os.CreateTemp(cacheDir, ".windows-*.qcow2.partial")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, decoder); err != nil {
		return "", fmt.Errorf("decompress Windows image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := validateBaseImage(ctx, tmp.Name()); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0444); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

func r2Request(ctx context.Context, client *http.Client, cfg WindowsConfig, objectURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, objectURL, nil)
	if err != nil {
		return nil, err
	}
	emptyHash := sha256.Sum256(nil)
	req.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(emptyHash[:]))
	err = v4.NewSigner().SignHTTP(ctx, aws.Credentials{AccessKeyID: cfg.R2AccessKey, SecretAccessKey: cfg.R2SecretKey}, req, hex.EncodeToString(emptyHash[:]), "s3", "auto", time.Now())
	if err != nil {
		return nil, fmt.Errorf("sign R2 request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request Windows image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("R2 image: HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func validateBaseImage(ctx context.Context, path string) error {
	out, err := exec.CommandContext(ctx, "qemu-img", "info", "--output=json", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("inspect base image: %w: %s", err, out)
	}
	var info struct {
		Format  string `json:"format"`
		Backing string `json:"backing-filename"`
	}
	if err := json.Unmarshal(out, &info); err != nil {
		return err
	}
	if info.Format != "qcow2" || info.Backing != "" {
		return fmt.Errorf("base image must be a standalone qcow2")
	}
	return nil
}

// CreateOverlay never writes to the shared backing image.
func CreateOverlay(ctx context.Context, base, overlay string) error {
	absolute, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", absolute, overlay).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create qcow2 overlay: %w: %s", err, out)
	}
	return nil
}
