package dp_wireguard

import "os/exec"

func InitWindows(path string) error {
	return exec.Command("wireguard", "/installtunnelservice", path).Run()
}

func CleanupWindows(path string) error {
	return exec.Command("wireguard", "/installtunnelservice", path).Run()
}
