package dp_wireguard

import (
	"os/exec"
	"strings"
)

func InitWgQuick(path string) error {
	out, err := exec.Command("wg-quick", "up", path).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "already exists") {
			err := CleanupWgQuick(path)
			if err != nil {
				return err
			}
			out, err = exec.Command("wg-quick", "up", path).CombinedOutput()
		}
	}
	return err
}

func CleanupWgQuick(path string) error {
	return exec.Command("wg-quick", "down", path).Run()
}
