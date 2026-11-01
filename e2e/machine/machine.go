// Package machine provides the machines and guest operations used by e2e tests.
// The test runner lives on Linux; a guest may be a Linux container or Windows VM.
package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type OS string

const (
	Linux   OS = "linux"
	Windows OS = "windows"
)

type Info struct {
	OS      OS
	IP      string // Address on the test data network, never the management NIC.
	WorkDir string
}

// Command passes arguments separately. Shell syntax is only interpreted when
// the executable is explicitly a shell.
type Command struct {
	Path string
	Args []string
	Dir  string
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func (r Result) check(err error) (Result, error) {
	if err != nil {
		return r, err
	}
	if r.ExitCode != 0 {
		return r, fmt.Errorf("guest command exited with code %d: %s\n%s", r.ExitCode, r.Stdout, r.Stderr)
	}
	return r, nil
}

type Machine interface {
	Info() Info
	Run(context.Context, Command) (Result, error)
	WriteFile(context.Context, string, []byte) error
	Start(context.Context, Command) (Process, error)
	Close(context.Context) error
}

// Process survives the command session that launched it. Output is a cumulative
// snapshot; callers can subscribe by retaining their last byte offset.
type Process interface {
	Poll(context.Context) (output Result, exited bool, err error)
	Stop(context.Context) error
}

func guestPath(m Machine, name string) string {
	sep := "/"
	if m.Info().OS == Windows {
		sep = `\`
	}
	return strings.TrimRight(m.Info().WorkDir, `/\`) + sep + name
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func psQuote(s string) string    { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Both backends return base64 output in JSON so arbitrary bytes survive transport.
func processOutput(data string) (Result, bool, error) {
	var snapshot struct {
		Stdout, Stderr []byte
		Code           *int
	}
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return Result{}, false, fmt.Errorf("decode process output: %w", err)
	}
	result := Result{Stdout: string(snapshot.Stdout), Stderr: string(snapshot.Stderr)}
	if snapshot.Code != nil {
		result.ExitCode = *snapshot.Code
	}
	return result, snapshot.Code != nil, nil
}
