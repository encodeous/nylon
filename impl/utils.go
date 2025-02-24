package impl

import (
	"github.com/encodeous/nylon/state"
	"reflect"
)

func AddMetric(a, b uint16) uint16 {
	if a == state.INF || b == state.INF {
		return state.INF
	} else {
		return uint16(min(int64(state.INF-1), int64(a)+int64(b)))
	}
}

func SeqnoLt(a, b uint16) bool {
	x := (b - a + 63336) % 63336
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

func IsFeasible(curRoute *state.Route, newRoute state.PubRoute, metric uint16) bool {
	if SeqnoLt(newRoute.Src.Seqno, curRoute.Src.Seqno) {
		return false
	}

	if metric == state.INF {
		return false
	}

	if metric < curRoute.Fd ||
		SeqnoLt(curRoute.Src.Seqno, newRoute.Src.Seqno) ||
		(metric == curRoute.Fd && (curRoute.PubMetric == state.INF || curRoute.Retracted)) {
		return true
	}
	return false
}

func SwitchHeuristic(curRoute *state.Route, newRoute state.PubRoute, metric uint16, metRange uint16) bool {
	// prevent oscillation
	curMetric := float64(curRoute.PubMetric)
	newMetric := float64(metric)
	if (newMetric+float64(metRange))*state.LinkSwitchMetricCostMultiplier > curMetric {
		return false
	}
	return true
}

func Get[T state.NyModule](s *state.State) T {
	t := reflect.TypeFor[T]()
	return s.Modules[t.String()].(T)
}
