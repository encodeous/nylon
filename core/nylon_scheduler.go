package core

import (
	"fmt"
	"reflect"
	"runtime"
	"time"
)

// Dispatch Dispatches the function to run on the main thread without waiting for it to complete
func (n *Nylon) Dispatch(fun func() error) {
	defer func() {
		if r := recover(); r != nil {
			n.Cancel(fmt.Errorf("dispatch panic: %v", r))
		}
	}()
	for {
		select {
		case n.DispatchChannel <- fun:
			return
		default:
			n.Log.Error("dispatch channel is full, discarded function", "fun", runtime.FuncForPC(reflect.ValueOf(fun).Pointer()).Name(), "len", len(n.DispatchChannel))
			return
		}
	}
}

func (n *Nylon) ScheduleTask(fun func() error, delay time.Duration) {
	time.AfterFunc(delay, func() {
		n.Dispatch(fun)
	})
}

func (n *Nylon) repeatedTask(fun func() error, delay time.Duration) {
	// run immediately
	n.Dispatch(fun)
	ticker := time.NewTicker(delay)
	defer ticker.Stop()
	for n.Context.Err() == nil {
		select {
		case <-n.Context.Done():
			return
		case <-ticker.C:
			n.Dispatch(fun)
		}
	}
}

func (n *Nylon) RepeatTask(fun func() error, delay time.Duration) {
	go n.repeatedTask(fun, delay)
}
