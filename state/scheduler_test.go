package state

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatchChan := make(chan func(*State) error, 10)
	env := &Env{
		DispatchChannel: dispatchChan,
		Context:         ctx,
		Cancel: func(err error) {
			cancel()
		},
	}
	state := &State{
		Env: env,
	}

	var called bool

	go func() {
		select {
		case f := <-dispatchChan:
			if err := f(state); err != nil {
				t.Errorf("Dispatch error: %v", err)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Timed out waiting for dispatched function")
		}
	}()

	env.Dispatch(func(s *State) error {
		called = true
		return nil
	})

	time.Sleep(150 * time.Millisecond)

	if !called {
		t.Fatal("Dispatch function was not executed")
	}
}

func TestScheduleTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatchChan := make(chan func(*State) error, 10)
	env := &Env{
		DispatchChannel: dispatchChan,
		Context:         ctx,
		Cancel: func(err error) {
			cancel()
		},
	}
	state := &State{
		Env: env,
	}

	var taskCalled bool

	env.ScheduleTask(func(s *State) error {
		taskCalled = true
		return nil
	}, 50*time.Millisecond)

	// Wait enough time for the scheduled task to be dispatched.
	time.Sleep(100 * time.Millisecond)
	select {
	case f := <-dispatchChan:
		if err := f(state); err != nil {
			t.Errorf("Scheduled task error: %v", err)
		}
	default:
		t.Fatal("No task was scheduled")
	}

	if !taskCalled {
		t.Fatal("Scheduled task was not executed")
	}
}

func TestRepeatTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dispatchChan := make(chan func(*State) error, 10)
	env := &Env{
		DispatchChannel: dispatchChan,
		Context:         ctx,
		Cancel: func(err error) {
			cancel()
		},
	}
	state := &State{
		Env: env,
	}

	var wg sync.WaitGroup
	wg.Add(3)
	var count int

	env.RepeatTask(func(s *State) error {
		count++
		wg.Done()
		if count >= 3 {
			cancel()
		}
		return nil
	}, 50*time.Millisecond)

	// Process the repeat tasks until context is cancelled.
loop:
	for {
		select {
		case f := <-dispatchChan:
			err := f(state)
			if err != nil {
				t.Fatalf("RepeatTask error: %v", err)
			}
		case <-ctx.Done():
			break loop
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Timed out waiting for RepeatTask to execute")
		}
	}
	wg.Wait()
	if count != 3 {
		t.Fatalf("Expected 3 executions, got %d", count)
	}
}
