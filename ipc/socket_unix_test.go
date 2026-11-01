//go:build !windows

package ipc

import (
	"net"
	"os"
	"testing"

	"github.com/encodeous/nylon/protocol"
	"github.com/stretchr/testify/require"
)

// useTempDir points the sockets at a fresh folder. Unix socket paths are short, so not t.TempDir().
func useTempDir(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "nylon-ipc")
	require.NoError(t, err)
	old := Dir
	Dir = dir
	t.Cleanup(func() {
		Dir = old
		_ = os.RemoveAll(dir)
	})
}

func TestListenIsOwnerOnly(t *testing.T) {
	useTempDir(t)
	l, err := Listen("nylon")
	require.NoError(t, err)
	defer l.Close()
	info, err := os.Stat(Path("nylon"))
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&0o077, "group and others must not have access, got %v", info.Mode().Perm())
}

func TestListenRefusesARunningDaemon(t *testing.T) {
	useTempDir(t)
	l, err := Listen("nylon")
	require.NoError(t, err)
	defer l.Close()
	_, err = Listen("nylon")
	require.ErrorContains(t, err, "already serving")
}

// A daemon that crashed leaves its socket behind; the next one replaces it.
func TestListenReplacesAStaleSocket(t *testing.T) {
	useTempDir(t)
	l, err := net.Listen("unix", Path("nylon"))
	require.NoError(t, err)
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, l.Close())
	_, err = os.Stat(Path("nylon"))
	require.NoError(t, err, "the stale socket should still exist")

	l, err = Listen("nylon")
	require.NoError(t, err)
	require.NoError(t, l.Close())
}

// The client against a minimal server over the real socket.
func TestDialAndDo(t *testing.T) {
	useTempDir(t)
	l, err := Listen("nylon")
	require.NoError(t, err)
	defer l.Close()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := ReadHello(conn); err != nil {
			return
		}
		_ = WriteHello(conn)
		req := &protocol.IpcRequest{}
		if ReadFrame(conn, req) != nil {
			return
		}
		_ = WriteFrame(conn, &protocol.IpcResponse{Id: req.Id, Error: Errorf(protocol.IpcError_NOT_FOUND, "no peer %s", req.GetProbe().GetPeerId())})
	}()

	c, err := Dial("nylon")
	require.NoError(t, err)
	defer c.Close()
	_, err = c.Do(&protocol.IpcRequest{Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: "bob"}}})
	var ipcErr *Error
	require.ErrorAs(t, err, &ipcErr)
	require.Equal(t, protocol.IpcError_NOT_FOUND, ipcErr.Code)
	require.Equal(t, "no peer bob", ipcErr.Message)
}
