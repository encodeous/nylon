package ipc

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/encodeous/nylon/protocol"
	"github.com/stretchr/testify/require"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	sent := &protocol.IpcRequest{Id: 7, Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: "bob"}}}
	require.NoError(t, WriteFrame(&buf, sent))
	require.Equal(t, uint32(buf.Len()-4), binary.BigEndian.Uint32(buf.Bytes()))

	got := &protocol.IpcRequest{}
	require.NoError(t, ReadFrame(&buf, got))
	require.Equal(t, uint32(7), got.Id)
	require.Equal(t, "bob", got.GetProbe().GetPeerId())
}

// A length over the limit is refused before anything is allocated for it.
func TestFrameTooLarge(t *testing.T) {
	frame := binary.BigEndian.AppendUint32(nil, MaxFrame+1)
	require.ErrorContains(t, ReadFrame(bytes.NewReader(frame), &protocol.IpcRequest{}), "over the")
}

func TestHello(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteHello(&buf))
	version, err := ReadHello(&buf)
	require.NoError(t, err)
	require.Equal(t, Version, version)

	_, err = ReadHello(bytes.NewReader([]byte("errno=")))
	require.ErrorIs(t, err, ErrNotNylon)
}
