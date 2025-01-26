package dp_wireguard

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"os/exec"
	"strings"
)

func InitWgQuick(path string, e *state.Env) error {
	e.Log.Debug("init wg-quick")
	out, err := exec.Command("wg-quick", "up", path).CombinedOutput()
	e.Log.Debug("wg-quick output", "out", string(out))
	if err != nil {
		if strings.Contains(string(out), "already exists") {
			err := CleanupWgQuick(path, e)
			if err != nil {
				return err
			}
			out, err = exec.Command("wg-quick", "up", path).CombinedOutput()
			e.Log.Debug("wg-quick output", "out", string(out))
		}
	}
	return err
}

func CleanupWgQuick(path string, e *state.Env) error {
	e.Log.Debug("cleaning up wg-quick")
	output, err := exec.Command("wg-quick", "down", path).CombinedOutput()
	e.Log.Debug("wg-quick output", "out", string(output))
	if err != nil {
		return fmt.Errorf("%v\n%s", err, string(output))
	}
	return nil
}
