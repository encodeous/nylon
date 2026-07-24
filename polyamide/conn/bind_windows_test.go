package conn

import (
	"math"
	"testing"
)

func TestRingBufferAvailable(t *testing.T) {
	tests := []struct {
		name string
		ring ringBuffer
		want uint32
	}{
		{
			name: "empty",
			want: packetsPerRing,
		},
		{
			name: "partially occupied",
			ring: ringBuffer{head: 3, tail: 13},
			want: packetsPerRing - 10,
		},
		{
			name: "wrapped counters",
			ring: ringBuffer{head: math.MaxUint32 - 4, tail: 5},
			want: packetsPerRing - 10,
		},
		{
			name: "full",
			ring: ringBuffer{isFull: true},
			want: 0,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.ring.available(); got != test.want {
				t.Fatalf("available() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRingBufferCancelPush(t *testing.T) {
	ring := ringBuffer{
		head:   10,
		tail:   11,
		isFull: true,
	}

	ring.cancelPush()

	if ring.tail != 10 {
		t.Fatalf("tail = %d, want 10", ring.tail)
	}
	if ring.isFull {
		t.Fatal("ring remains full after cancelling a push")
	}
	if got := ring.available(); got != packetsPerRing {
		t.Fatalf("available() = %d, want %d", got, packetsPerRing)
	}
}
