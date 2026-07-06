package core

import (
	"context"
	"errors"
	"testing"
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
