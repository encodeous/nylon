//go:build !windows

package ipc

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Dir holds the sockets, one per interface. Only root can use them.
var Dir = "/var/run/nylon"

// Path is where the daemon for interface itf listens.
func Path(itf string) string {
	return filepath.Join(Dir, itf+".sock")
}

// Listen opens the socket for interface itf. It fails if another daemon is already serving it, and replaces a
// socket left behind by one that exited.
func Listen(itf string) (net.Listener, error) {
	if err := os.MkdirAll(Dir, 0o755); err != nil {
		return nil, err
	}
	path := Path(itf)
	if conn, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("another nylon is already serving %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	// owner only, from the moment it exists
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	return net.Listen("unix", path)
}

func dial(itf string) (net.Conn, error) {
	return net.Dial("unix", Path(itf))
}
