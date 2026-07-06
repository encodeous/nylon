package core

import (
	"testing"
	"time"

	"github.com/encodeous/nylon/protocol"
)

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
