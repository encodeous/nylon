//go:build !linux

package machine

import (
	"context"
	"fmt"
)

func NewWindows(context.Context, WindowsConfig) (Machine, error) {
	return nil, fmt.Errorf("the QEMU Windows backend requires a local Linux host")
}

func PrepareWindowsImage(context.Context, WindowsConfig) (string, error) {
	return "", fmt.Errorf("the QEMU Windows backend requires a local Linux host")
}
