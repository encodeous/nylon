package state

import (
	"reflect"
	"testing"
)

func TestSortPairsInt(t *testing.T) {
	pairs := []Pair[int, int]{
		{V1: 3, V2: 10},
		{V1: 1, V2: 20},
		{V1: 1, V2: 5},
		{V1: 2, V2: 15},
	}
	expected := []Pair[int, int]{
		{V1: 1, V2: 5},
		{V1: 1, V2: 20},
		{V1: 2, V2: 15},
		{V1: 3, V2: 10},
	}
	SortPairs(pairs)
	if !reflect.DeepEqual(pairs, expected) {
		t.Fatalf("expected %v, got %v", expected, pairs)
	}
}

func TestSortPairsString(t *testing.T) {
	pairs := []Pair[string, string]{
		{V1: "b", V2: "y"},
		{V1: "a", V2: "z"},
		{V1: "a", V2: "x"},
		{V1: "c", V2: "w"},
	}
	expected := []Pair[string, string]{
		{V1: "a", V2: "x"},
		{V1: "a", V2: "z"},
		{V1: "b", V2: "y"},
		{V1: "c", V2: "w"},
	}
	SortPairs(pairs)
	if !reflect.DeepEqual(pairs, expected) {
		t.Fatalf("expected %v, got %v", expected, pairs)
	}
}
