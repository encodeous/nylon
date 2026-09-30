package adapter

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
)

type endpoint struct {
	owner  *Transport
	native conn.Endpoint
}

func (e *endpoint) Address() string { return e.native.DstToString() }

func (t *Transport) PrepareEndpoint(ctx context.Context, address string) (polyamide.Endpoint, error) {
	if t.closed.Load() {
		return nil, polyamide.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", address, polyamide.ErrInvalidEndpoint)
	}
	native, err := t.bind.ParseEndpoint(ap.String())
	if err != nil {
		return nil, fmt.Errorf("parse endpoint: %w", err)
	}
	return &endpoint{owner: t, native: native}, nil
}

func (t *Transport) resolveEndpoint(value polyamide.Endpoint) (conn.Endpoint, error) {
	if value == nil {
		return nil, nil
	}
	e, ok := value.(*endpoint)
	if !ok || e == nil || e.owner != t || e.native == nil {
		return nil, polyamide.ErrInvalidEndpoint
	}
	return e.native, nil
}

func (t *Transport) rememberEndpoint(native conn.Endpoint) polyamide.Endpoint {
	return &endpoint{owner: t, native: native}
}

// WireGuard needs an initial peer address for the handshake.
func (t *Transport) ensureEndpoint(p *peer, endpoint conn.Endpoint) error {
	p.endpointMu.Lock()
	defer p.endpointMu.Unlock()
	if len(p.raw.GetEndpoints()) != 0 {
		return nil
	}
	if endpoint == nil {
		return polyamide.ErrNoEndpoint
	}
	p.raw.SetEndpoints([]conn.Endpoint{endpoint})
	return nil
}
