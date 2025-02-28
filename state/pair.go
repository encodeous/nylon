package state

import (
	"cmp"
	"sort"
)

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

type Triple[Ty1, Ty2, Ty3 any] struct {
	V1 Ty1
	V2 Ty2
	V3 Ty3
}
