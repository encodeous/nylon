package test

import (
	"reflect"
	"testing"

	"github.com/encodeous/nylon/state"
)

func TestParseGraph_ValidGraph(t *testing.T) {
	// sample valid graph: "g = a, b" implies pairing "a" and "b"
	graph := []string{"g = a, b"}
	nodes := []string{"a", "b"}

	pairs, err := state.ParseGraph(graph, nodes)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	expected := []state.Pair[state.NodeId, state.NodeId]{}
	if !reflect.DeepEqual(pairs, expected) {
		t.Errorf("Expected %v, got %v", expected, pairs)
	}
}

func TestParseGraph_InvalidGraph(t *testing.T) {
	// sample invalid input that should produce an error
	graph := []string{"invalid graph"}
	nodes := []string{"a", "b"}

	_, err := state.ParseGraph(graph, nodes)
	if err == nil {
		t.Errorf("Expected error for invalid graph, got nil")
	}
}
