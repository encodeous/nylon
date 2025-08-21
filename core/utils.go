package core

import (
	"github.com/encodeous/nylon/state"
	"reflect"
)

func AddMetric(a, b uint16) uint16 {
	if a == state.INF || b == state.INF {
		return state.INF
	} else {
		return uint16(min(uint32(state.INFM), uint32(a)+uint32(b)))
	}
}

func SeqnoLt(a, b uint16) bool {
	x := b - a
	return 0 < x && x < 32768
}

func SeqnoLe(a, b uint16) bool {
	return a == b || SeqnoLt(a, b)
}
func SeqnoGt(a, b uint16) bool {
	return !SeqnoLe(a, b)
}
func SeqnoGe(a, b uint16) bool {
	return !SeqnoLt(a, b)
}

func Get[T state.NyModule](s *state.State) T {
	t := reflect.TypeFor[T]()
	return s.Modules[t.String()].(T)
}
