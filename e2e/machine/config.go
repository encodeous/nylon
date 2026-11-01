package machine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type WindowsConfig struct {
	Name, IP, Subnet, Bridge string
	// HostHelperImage supplies iproute2 in a privileged, host-networked Docker
	// helper. The Docker daemon must run locally on this Linux QEMU host.
	HostHelperImage string
	WorkDir         string
	BaseImage       string
	CacheDir        string
	R2Endpoint      string
	R2AccessKey     string
	R2SecretKey     string
	Bucket          string
	Object          string
	Username        string
	Password        string
	MemoryMiB       int
	CPUs            int
	Accelerator     string
	NICModel        string
	UEFICode        string
	UEFIVars        string
	BootTimeout     time.Duration
}

// WindowsConfigFromEnv reads .env without changing the process environment.
// Exported environment variables take precedence, so the same code works in CI.
func WindowsConfigFromEnv(root string) (WindowsConfig, error) {
	values, err := godotenv.Read(filepath.Join(root, ".env"))
	if err != nil && !os.IsNotExist(err) {
		return WindowsConfig{}, err
	}
	get := func(key, fallback string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		if value, ok := values[key]; ok {
			return value
		}
		return fallback
	}
	cfg := WindowsConfig{
		BaseImage:   get("NYLON_E2E_WINDOWS_IMAGE", ""),
		CacheDir:    get("NYLON_E2E_IMAGE_CACHE", filepath.Join(root, "workdir", "e2e", "images")),
		WorkDir:     filepath.Join(root, "workdir", "e2e", "vms"),
		R2Endpoint:  get("AWS_ENDPOINT_URL_S3", ""),
		R2AccessKey: get("AWS_ACCESS_KEY_ID", ""),
		R2SecretKey: get("AWS_SECRET_ACCESS_KEY", ""),
		Bucket:      get("NYLON_E2E_WINDOWS_BUCKET", "nylon-ci-windows-image"),
		Object:      get("NYLON_E2E_WINDOWS_OBJECT", "nylon-ci-win2025.qcow2.zst"),
		Username:    get("NYLON_E2E_WINRM_USERNAME", "Administrator"),
		Password:    get("NYLON_E2E_WINRM_PASSWORD", "nylon"),
		Accelerator: get("NYLON_E2E_QEMU_ACCEL", "kvm"),
		NICModel:    get("NYLON_E2E_QEMU_NIC", "virtio-net-pci"),
		UEFICode:    get("NYLON_E2E_UEFI_CODE", "/usr/share/OVMF/OVMF_CODE_4M.fd"),
		UEFIVars:    get("NYLON_E2E_UEFI_VARS", "/usr/share/OVMF/OVMF_VARS_4M.fd"),
		BootTimeout: 5 * time.Minute,
	}
	for _, field := range []struct {
		key, fallback string
		dst           *int
	}{
		{"NYLON_E2E_WINDOWS_MEMORY_MIB", "2048", &cfg.MemoryMiB},
		{"NYLON_E2E_WINDOWS_CPUS", "2", &cfg.CPUs},
	} {
		value, err := strconv.Atoi(get(field.key, field.fallback))
		if err != nil || value < 1 {
			return WindowsConfig{}, fmt.Errorf("%s must be a positive integer", field.key)
		}
		*field.dst = value
	}
	for _, path := range []*string{&cfg.BaseImage, &cfg.CacheDir, &cfg.UEFICode, &cfg.UEFIVars} {
		if *path != "" && !filepath.IsAbs(*path) {
			*path = filepath.Join(root, *path)
		}
	}
	return cfg, nil
}
