package core

import (
	"github.com/encodeous/nylon/log"
	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/polyamide-wireguard-go/adapter"
	"github.com/encodeous/polyamide-wireguard-go/conn"
)

// NewWireGuardTransport creates the WireGuard transport for n. A nil newBind uses the platform UDP bind.
func NewWireGuardTransport(n *Nylon, newBind func() conn.Bind) (*adapter.Transport, error) {
	return adapter.New(adapter.Options{PrivateKey: polyamide.PrivateKey(n.Key), ListenPort: n.Port, NewBind: newBind, Logger: n.Log.With("module", log.ScopePolyamide), Debug: n.DBG_log_wireguard})
}
