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
