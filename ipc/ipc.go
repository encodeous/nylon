// Package ipc is nylon's control protocol: the socket the daemon listens on, and how messages cross it.
// protocol/nylon_ipc.proto describes the wire format.
package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/encodeous/nylon/protocol"
	"google.golang.org/protobuf/proto"
)

// Version is the protocol version
const Version uint16 = 1

// MaxFrame bounds a frame, so a bad peer can't make the other side allocate without limit.
const MaxFrame = 16 << 20

var magic = [4]byte{'N', 'Y', 'L', 'N'}

// ErrVersion means the other side speaks a different protocol version.
type ErrVersion struct{ Local, Remote uint16 }

func (e ErrVersion) Error() string {
	return fmt.Sprintf("nylon IPC version mismatch: this side speaks %d, the other speaks %d", e.Local, e.Remote)
}

var ErrNotNylon = errors.New("not a nylon IPC socket")

// WriteHello sends this side's hello.
func WriteHello(w io.Writer) error {
	var hello [6]byte
	copy(hello[:4], magic[:])
	binary.BigEndian.PutUint16(hello[4:], Version)
	_, err := w.Write(hello[:])
	return err
}

// ReadHello reads the other side's hello and returns its version.
func ReadHello(r io.Reader) (uint16, error) {
	var hello [6]byte
	if _, err := io.ReadFull(r, hello[:]); err != nil {
		return 0, err
	}
	if !bytes.Equal(hello[:4], magic[:]) {
		return 0, ErrNotNylon
	}
	return binary.BigEndian.Uint16(hello[4:]), nil
}

// WriteFrame sends m as one frame. Callers sharing a writer must serialize calls.
func WriteFrame(w io.Writer, m proto.Message) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) > MaxFrame {
		return fmt.Errorf("frame of %d bytes is over the %d byte limit", len(data), MaxFrame)
	}
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame, uint32(len(data)))
	copy(frame[4:], data)
	_, err = w.Write(frame)
	return err
}

// ReadFrame reads one frame into m.
func ReadFrame(r io.Reader, m proto.Message) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > MaxFrame {
		return fmt.Errorf("frame of %d bytes is over the %d byte limit", n, MaxFrame)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return proto.Unmarshal(data, m)
}

// Error is a request the daemon couldn't do.
type Error struct {
	Code    protocol.IpcError_Code
	Message string
}

func (e *Error) Error() string { return e.Message }

// Errorf builds a response error for a request.
func Errorf(code protocol.IpcError_Code, format string, args ...any) *protocol.IpcError {
	return &protocol.IpcError{Code: code, Message: fmt.Sprintf(format, args...)}
}
