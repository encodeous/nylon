package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFutureAwaitOnce(t *testing.T) {
	future, complete := NewFuture[int]()
	complete(7, nil)

	value, err := future.Await(context.Background())
	if err != nil {
		t.Fatalf("first await failed: %v", err)
	}
	if value != 7 {
		t.Fatalf("expected 7, got %d", value)
	}

	_, err = future.Await(context.Background())
	if !errors.Is(err, ErrFutureAlreadyAwaited) {
		t.Fatalf("expected ErrFutureAlreadyAwaited, got %v", err)
	}
}

func TestFutureAwaitRejectsConcurrentWaiter(t *testing.T) {
	future, complete := NewFuture[int]()
	firstValue := make(chan int, 1)
	firstErr := make(chan error, 1)

	go func() {
		value, err := future.Await(context.Background())
		if err != nil {
			firstErr <- err
			return
		}
		firstValue <- value
	}()

	waitForFutureAwaiting(t, future)

	_, err := future.Await(context.Background())
	if !errors.Is(err, ErrFutureAlreadyAwaited) {
		t.Fatalf("expected ErrFutureAlreadyAwaited, got %v", err)
	}

	complete(9, nil)
	select {
	case err := <-firstErr:
		t.Fatalf("first await failed: %v", err)
	case value := <-firstValue:
		if value != 9 {
			t.Fatalf("expected first await to get 9, got %d", value)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first await")
	}
}

func TestFutureAwaitCancelDoesNotConsumeResult(t *testing.T) {
	future, complete := NewFuture[int]()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := future.Await(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}

	complete(11, nil)
	value, err := future.Await(context.Background())
	if err != nil {
		t.Fatalf("second await failed: %v", err)
	}
	if value != 11 {
		t.Fatalf("expected 11, got %d", value)
	}
}

func TestFutureAllPreservesOrder(t *testing.T) {
	a, completeA := NewFuture[int]()
	b, completeB := NewFuture[int]()

	completeB(2, nil)
	completeA(1, nil)

	values, err := AwaitAll(context.Background(), []Future[int]{a, b})
	if err != nil {
		t.Fatalf("await all failed: %v", err)
	}
	if len(values) != 2 || values[0] != 1 || values[1] != 2 {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestFutureAllTimeout(t *testing.T) {
	future, _ := NewFuture[int]()
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()

	_, err := AwaitAll(ctx, []Future[int]{future})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func waitForFutureAwaiting[T any](t *testing.T, future Future[T]) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		future.state.mu.Lock()
		awaiting := future.state.awaiting
		future.state.mu.Unlock()
		if awaiting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for future to have a waiter")
}
