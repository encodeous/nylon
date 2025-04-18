package state

import (
	"fmt"
	"time"
)

// Dispatch Dispatches the function to run on the main thread without waiting for it to complete
func (e *Env) Dispatch(fun func(*State) error) {
	defer func() {
		if r := recover(); r != nil {
			e.Cancel(fmt.Errorf("dispatch panic: %v", r))
		}
	}()
	select {
	case e.DispatchChannel <- fun:
	default:
		e.Cancel(fmt.Errorf("dispatch channel is full"))
	}

}

// DispatchWait Dispatches the function to run on the main thread and wait for it to complete
func (e *Env) DispatchWait(fun func(*State) (any, error)) (any, error) {
	ret := make(chan Pair[any, error])
	e.Dispatch(func(s *State) error {
		res, err := fun(s)
		ret <- Pair[any, error]{res, err}
		return err
	})
	select {
	case res := <-ret:
		return res.V1, res.V2
	case <-e.Context.Done():
		return nil, e.Context.Err()
	}
}

func (e *Env) ScheduleTask(fun func(*State) error, delay time.Duration) {
	time.AfterFunc(delay, func() {

		e.Dispatch(fun)
	})
}

func (e *Env) repeatedTask(fun func(*State) error, delay time.Duration) {
	for e.Context.Err() == nil {
		select {
		case <-e.Context.Done():
			return
		case <-time.After(delay):
			e.Dispatch(fun)
		}
	}
}

func (e *Env) RepeatTask(fun func(*State) error, delay time.Duration) {
	go e.repeatedTask(fun, delay)
}
