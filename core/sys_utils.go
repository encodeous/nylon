package core

import (
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

func ExecSplit(logger *slog.Logger, command string) error {
	parts := strings.Split(command, " ")
	return Exec(logger, parts[0], parts[1:]...)
}

func Exec(logger *slog.Logger, name string, arg ...string) error {
	out, err := exec.Command(name, arg...).CombinedOutput()
	logger.Debug("exec command", "cmd", name, "arg", arg, "out", string(out))
	if err != nil {
		return fmt.Errorf("error executing command: %s %s. %w. Output: %s", name, arg, err, out)
	}
	return nil
}
