package state

import (
	"fmt"
	"reflect"
	"runtime"
	"time"
)

// Dispatch Dispatches the function to run on the main thread without waiting for it to complete
func (e *Env) Dispatch(fun func(*State) error) {
	defer func() {
		if r := recover(); r != nil {
			e.Cancel(fmt.Errorf("dispatch panic: %v", r))
		}
	}()
	for {
		select {
		case e.DispatchChannel <- fun:
			return
		default:
			e.Log.Error("dispatch channel is full, discarded function", "fun", runtime.FuncForPC(reflect.ValueOf(fun).Pointer()).Name(), "len", len(e.DispatchChannel))
			return
		}
	}
}

func (e *Env) ScheduleTask(fun func(*State) error, delay time.Duration) {
	time.AfterFunc(delay, func() {
		e.Dispatch(fun)
	})
}

func (e *Env) repeatedTask(fun func(*State) error, delay time.Duration) {
	// run immediately
	e.Dispatch(fun)
	ticker := time.NewTicker(delay)
	for e.Context.Err() == nil {
		select {
		case <-e.Context.Done():
			return
		case <-ticker.C:
			e.Dispatch(fun)
		}
	}
}

func (e *Env) RepeatTask(fun func(*State) error, delay time.Duration) {
	go e.repeatedTask(fun, delay)
}
