package core

import (
	"net/netip"
	"testing"
	"time"

	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/require"
)

func TestBuildNodes(t *testing.T) {
	n := &Nylon{}
	n.CentralCfg = state.CentralCfg{
		Routers: []state.RouterCfg{{NodeCfg: state.NodeCfg{
			Id:        "a",
			Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("fd00::1")},
		}}},
		Clients: []state.ClientCfg{{NodeCfg: state.NodeCfg{Id: "phone"}}},
	}

	nodes := buildNodes(n)
	require.Len(t, nodes, 2)
	require.Equal(t, "a", nodes[0].NodeId)
	require.Equal(t, []string{"10.0.0.1", "fd00::1"}, nodes[0].Addresses)
	require.Equal(t, "phone", nodes[1].NodeId)
	require.Empty(t, nodes[1].Addresses)
}

func TestIPCProbeTimeout(t *testing.T) {
	tests := []struct {
		name      string
		timeoutMs uint32
		want      time.Duration
	}{
		{
			name: "default",
			want: defaultIPCProbeTimeout,
		},
		{
			name:      "user value",
			timeoutMs: 1500,
			want:      1500 * time.Millisecond,
		},
		{
			name:      "capped",
			timeoutMs: uint32((maxIPCProbeTimeout + time.Second) / time.Millisecond),
			want:      maxIPCProbeTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ipcProbeTimeout(&protocol.ProbeRequest{TimeoutMs: tt.timeoutMs})
			if got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
