package state

import (
	"cmp"
	"sort"
)

// Pair is only used in-memory, not serialized
type Pair[Ty1, Ty2 any] struct {
	V1 Ty1
	V2 Ty2
}

func SortPairs[T cmp.Ordered](pairs []Pair[T, T]) {
	sort.Slice(pairs, func(i, j int) bool {
		x := cmp.Compare(pairs[i].V1, pairs[j].V1)
		y := cmp.Compare(pairs[i].V2, pairs[j].V2)
		return x < 0 || x == 0 && y < 0
	})
}
