package state

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseLocalConfigRejectsDistribution(t *testing.T) {
	for _, dist := range []string{"{}", "null", "\n  url: https://example.com/bundle"} {
		_, err := ParseLocalConfig([]byte("id: node-1\ndist: " + dist + "\n"))
		require.ErrorContains(t, err, "dist is no longer supported in node.yaml")
	}

	cfg, err := ParseLocalConfig([]byte("id: node-1\nport: 57175\n"))
	require.NoError(t, err)
	require.Equal(t, NodeId("node-1"), cfg.Id)
}
