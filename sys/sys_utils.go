package sys

import (
	"fmt"
	"os/exec"
)

func Exec(name string, arg ...string) error {
	out, err := exec.Command(name, arg...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("error executing command: %s %s. %w. Output: %s", name, arg, err, out)
	}
	return nil
}
