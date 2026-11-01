package machine

import (
	"context"
	"strings"
	"testing"

	"github.com/encodeous/nylon/state"
)

type failedProcess struct {
	stopped bool
}

func (p *failedProcess) Poll(context.Context) (Result, bool, error) {
	return Result{Stderr: "Nylon has been initialized\nprocess crashed", ExitCode: 42}, true, nil
}
func (p *failedProcess) Stop(context.Context) error { p.stopped = true; return nil }

type testMachine struct{ process Process }

func (m *testMachine) Info() Info                                      { return Info{OS: Linux, WorkDir: "/app/config"} }
func (m *testMachine) Run(context.Context, Command) (Result, error)    { return Result{}, nil }
func (m *testMachine) Start(context.Context, Command) (Process, error) { return m.process, nil }
func (m *testMachine) WriteFile(context.Context, string, []byte) error { return nil }
func (m *testMachine) Close(context.Context) error                     { return nil }

func TestRunNylonRejectsExitedProcessEvenWithInitializationLog(t *testing.T) {
	p := &failedProcess{}
	m := &testMachine{process: p}
	_, err := RunNylon(t.Context(), m, state.CentralCfg{}, state.LocalCfg{}, NylonOptions{})
	if err == nil || !strings.Contains(err.Error(), "42") {
		t.Fatalf("expected startup exit failure, got %v", err)
	}
	if !p.stopped {
		t.Fatal("failed startup process was not cleaned up")
	}
}
