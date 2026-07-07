package core

import (
	"context"
	"errors"
	"sync"
)

var ErrFutureAlreadyAwaited = errors.New("future already awaited")

type futureState[T any] struct {
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	value    T
	err      error
	awaiting bool
	awaited  bool
}

// Future is a single-assignment async result.
type Future[T any] struct {
	state *futureState[T]
}

// NewFuture returns a Future and its completion function. Only the first
// completion call is used; later calls are ignored.
func NewFuture[T any]() (Future[T], func(T, error)) {
	state := &futureState[T]{done: make(chan struct{})}
	complete := func(value T, err error) {
		state.once.Do(func() {
			state.mu.Lock()
			state.value = value
			state.err = err
			state.mu.Unlock()
			close(state.done)
		})
	}
	return Future[T]{state: state}, complete
}

// Await blocks until the future completes or ctx is canceled. Only one Await
// call may wait for or consume a result at a time; concurrent calls and calls
// after the result has been consumed return ErrFutureAlreadyAwaited.
func (f Future[T]) Await(ctx context.Context) (T, error) {
	if err := f.beginAwait(); err != nil {
		var zero T
		return zero, err
	}
	select {
	case <-f.state.done:
		return f.finishAwait()
	default:
	}
	select {
	case <-f.state.done:
		return f.finishAwait()
	case <-ctx.Done():
		f.cancelAwait()
		var zero T
		return zero, ctx.Err()
	}
}

func (f Future[T]) beginAwait() error {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	if f.state.awaited || f.state.awaiting {
		return ErrFutureAlreadyAwaited
	}
	f.state.awaiting = true
	return nil
}

func (f Future[T]) finishAwait() (T, error) {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.awaiting = false
	f.state.awaited = true
	return f.state.value, f.state.err
}

func (f Future[T]) cancelAwait() {
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.awaiting = false
}

// Done returns a receive-only readiness channel for use in select statements.
// Receive from Done, then call Await to consume the result.
func (f Future[T]) Done() <-chan struct{} {
	return f.state.done
}

// AwaitAll blocks until every input future completes or ctx is canceled.
// Values are returned in the same order as the input slice. AwaitAll awaits and
// consumes every completed input future in order.
func AwaitAll[T any](ctx context.Context, futures []Future[T]) ([]T, error) {
	values := make([]T, len(futures))
	var firstErr error
	for idx, item := range futures {
		value, err := item.Await(ctx)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
				return nil, ctxErr
			}
			if firstErr == nil {
				firstErr = err
			}
		}
		values[idx] = value
	}
	return values, firstErr
}
