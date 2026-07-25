//go:build linux

package conn

import (
	"errors"
	"net"
	"sync"
	"testing"

	"golang.org/x/net/ipv6"
)

type batchReaderFunc func([]ipv6.Message, int) (int, error)

func (f batchReaderFunc) ReadBatch(msgs []ipv6.Message, flags int) (int, error) {
	return f(msgs, flags)
}

func TestStdNetBindReceiveCleansAllMessages(t *testing.T) {
	bind := NewStdNetBind().(*StdNetBind)
	msgs := make([]ipv6.Message, IdealBatchSize)
	for i := range msgs {
		msgs[i].Buffers = make(net.Buffers, 1)
		msgs[i].OOB = make([]byte, 0, stickyControlSize+gsoControlSize)
	}
	bind.msgsPool = sync.Pool{
		New: func() any {
			return &msgs
		},
	}

	wantErr := errors.New("stop after touching tail descriptor")
	reader := batchReaderFunc(func(readMsgs []ipv6.Message, _ int) (int, error) {
		if len(readMsgs) != IdealBatchSize/udpSegmentMaxDatagrams {
			t.Fatalf("ReadBatch received %d messages, want %d", len(readMsgs), IdealBatchSize/udpSegmentMaxDatagrams)
		}
		msg := &readMsgs[len(readMsgs)-1]
		msg.N = 23
		msg.NN = 1
		msg.Addr = &net.UDPAddr{}
		msg.Flags = 1
		msg.OOB = append(msg.OOB, 1)
		return 0, wantErr
	})

	_, err := bind.receiveIP(
		reader,
		nil,
		true,
		[][]byte{make([]byte, 64)},
		make([]int, 1),
		make([]Endpoint, 1),
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("receiveIP error = %v, want %v", err, wantErr)
	}

	got := msgs[len(msgs)-1]
	if got.N != 0 || got.NN != 0 || got.Addr != nil || got.Flags != 0 || len(got.OOB) != 0 {
		t.Fatalf("tail message was not reset: %+v", got)
	}
}
