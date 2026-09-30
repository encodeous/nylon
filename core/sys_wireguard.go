package core

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"

	"github.com/encodeous/nylon/log"
	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/polyamide-wireguard-go/adapter"
	"github.com/encodeous/polyamide-wireguard-go/conn"
	"github.com/encodeous/polyamide-wireguard-go/device"
)

// NewWireGuardTransport creates the WireGuard transport for n and serves Nylon IPC
// over its UAPI socket. A nil newBind uses the platform UDP bind.
func NewWireGuardTransport(n *Nylon, newBind func() conn.Bind) (*adapter.Transport, error) {
	return adapter.New(adapter.Options{PrivateKey: polyamide.PrivateKey(n.Key), ListenPort: n.Port, NewBind: newBind, Logger: n.Log.With("module", log.ScopePolyamide), Debug: n.DBG_log_wireguard,
		ControlHandlers: map[string]func(*bufio.ReadWriter) error{"get=nylon\n": func(w *bufio.ReadWriter) error {
			err := HandleNylonIPC(n, w)
			if errors.Is(err, ErrIPCStatusHandled) {
				return device.ErrIPCStatusHandled
			}
			return err
		}},
	})
}

// legacyWireGuard serves WireGuard's UAPI, which the wg tool and Nylon's IPC use.
// It exists for compatibility: only the platform runtimes create it, and it only
// covers the WireGuard transport.
type legacyWireGuard struct {
	transport *adapter.Transport
	listener  net.Listener // nil when there is no UAPI socket
	workers   sync.WaitGroup
}

// serve accepts UAPI connections until the listener closes.
func (w *legacyWireGuard) serve(ctx context.Context, logger *slog.Logger) {
	if w.listener == nil {
		return
	}
	w.workers.Add(1)
	go func() {
		defer w.workers.Done()
		for ctx.Err() == nil {
			c, err := w.listener.Accept()
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
					return
				}
				logger.Debug("UAPI accept failed", "err", err)
				continue
			}
			w.workers.Add(1)
			go func() {
				defer w.workers.Done()
				defer c.Close()
				stop := context.AfterFunc(ctx, func() { _ = c.Close() })
				defer stop()
				w.transport.HandleUAPI(c)
			}()
		}
	}()
}

func (w *legacyWireGuard) close() error {
	var err error
	if w.listener != nil {
		err = w.listener.Close()
	}
	w.workers.Wait()
	return err
}

// HandleUAPI serves one legacy UAPI connection with the WireGuard transport.
func (n *Nylon) HandleUAPI(c net.Conn) error {
	if n.wireGuard == nil {
		return polyamide.ErrUnsupported
	}
	n.wireGuard.transport.HandleUAPI(c)
	return nil
}
