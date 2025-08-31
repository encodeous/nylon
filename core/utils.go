package core

import (
	"reflect"

	"github.com/encodeous/nylon/state"
)

func AddMetric(a, b uint32) uint32 {
	if a == state.INF || b == state.INF {
		return state.INF
	} else {
		return uint32(min(uint64(state.INFM), uint64(a)+uint64(b)))
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

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}
