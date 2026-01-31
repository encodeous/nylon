package state

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseGraph_SimpleGraph(t *testing.T) {
	nodes := []string{"1", "2", "3", "4", "5"}
	input := `1, 2
3, 4
1,3,5`
	pairs, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.NoError(t, err)
	assert.ElementsMatch(t, pairs, []Pair[NodeId, NodeId]{
		{"1", "2"},
		{"3", "4"},
		{"1", "3"},
		{"3", "5"},
		{"1", "5"},
	})
}

func TestParseGraph_Groups(t *testing.T) {
	nodes := []string{"1", "2", "3", "4", "5", "6", "7"}
	input := `a = 1,2
b=3,,,4
c=5,6
d=a,b
d,d
7,d`
	pairs, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.NoError(t, err)
	assert.ElementsMatch(t, pairs, []Pair[NodeId, NodeId]{
		// d,d
		{"1", "2"},
		{"1", "3"},
		{"1", "4"},
		{"2", "3"},
		{"2", "4"},
		{"3", "4"},
		// 7,d
		{"1", "7"},
		{"2", "7"},
		{"3", "7"},
		{"4", "7"},
	})
}

func TestParseGraph_Cycle(t *testing.T) {
	nodes := []string{}
	input := `a = b
b = c
c = a`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "cycle detected in graph: [a b c]")
}

func TestParseGraph_DupGroupName(t *testing.T) {
	nodes := []string{}
	input := `a = b
a = b
b = b`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "duplicate group name: a")
}

func TestParseGraph_SymbolError(t *testing.T) {
	nodes := []string{"1"}
	input := `a = 1
b = 2`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "2 is not a valid node/group")
}

func TestParseGraph_EmptyGroup(t *testing.T) {
	nodes := []string{"1"}
	input := `a =`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "node/group list must not be empty")
}

func TestParseGraph_GroupNameIsNodeName(t *testing.T) {
	nodes := []string{"1"}
	input := `1 = 1`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "group name must not be a node name: 1")
}

func TestParseGraph_InvalidGroupDefinition(t *testing.T) {
	nodes := []string{"1"}
	input := `a = 1 = b`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, ". group definition must contain one '='")
}

func TestParseGraph_Single(t *testing.T) {
	nodes := []string{"1", "2", "3", "4", "5"}
	input := `1`
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "invalid pairing, [1]")
}

func TestParseGraph_None(t *testing.T) {
	nodes := []string{"1", "2", "3", "4", "5"}
	input := ``
	_, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.ErrorContains(t, err, "node/group list must not be empty")
}

func TestParseGraph_GroupsDeep(t *testing.T) {
	nodes := []string{"1", "2", "3", "4", "5", "6", "7"}
	input := `a = 1,2
b = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
c = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
d = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
e = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
f = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
g = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
h = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
i = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
j = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
k = a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a,a
k,k,3`
	pairs, err := ParseGraph(strings.Split(input, "\n"), nodes)
	assert.NoError(t, err)
	assert.ElementsMatch(t, pairs, []Pair[NodeId, NodeId]{
		{"1", "2"},
		{"1", "3"},
		{"2", "3"},
	})
}

func failGraph(t *testing.T, graph string) {
	_, err := ParseGraph(strings.Split(graph, "\n"), []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"})
	assert.Error(t, err)
}

func TestParseGraph_InvalidGraph(t *testing.T) {
	failGraph(t, `this graph is a baddie`)
	failGraph(t, `=========,,,,`)
	failGraph(t, `#`)
	failGraph(t, `\n\n\n\n\n\n`)
	failGraph(t, `1`)
	failGraph(t, `1,2,3,4,5,6,a`)
	failGraph(t, `1,2,3,4,5,6,7,8,9,10,11,12,13,14,15`)
	failGraph(t, `,,,,,,,,,,,,,,,,`)
	failGraph(t, `a=a`)
}

func TestSubtractPrefixDirect(t *testing.T) {
	includes := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.2/32"),
		netip.MustParsePrefix("10.0.0.3/32"),
	}
	excludes := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.3/32"),
	}
	result := SubtractPrefix(includes, excludes)
	assert.ElementsMatch(t, result, []netip.Prefix{
		netip.MustParsePrefix("10.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.2/32"),
	})
}

func TestSubtractPrefixLargerRange(t *testing.T) {
	includes := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.1/32"),
		netip.MustParsePrefix("10.0.0.2/32"),
		netip.MustParsePrefix("10.0.0.3/32"),
	}
	excludes := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"),
	}
	result := SubtractPrefix(includes, excludes)
	assert.ElementsMatch(t, result, []netip.Prefix{})
}
