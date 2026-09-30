package adapter

import (
	"context"
	"testing"

	"github.com/encodeous/nylon/polyamide"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
	"github.com/stretchr/testify/require"
)

func TestEndpointRetainsNativeStateAndRejectsForeignInstance(t *testing.T) {
	transport := &Transport{bind: conn.NewDefaultBind(), configuredEndpoints: make(map[*peer][]conn.Endpoint)}
	endpoint, err := transport.PrepareEndpoint(context.Background(), "127.0.0.1:1234")
	require.NoError(t, err)
	first, err := transport.resolveEndpoint(endpoint)
	require.NoError(t, err)
	cached, err := transport.resolveEndpoint(endpoint)
	require.NoError(t, err)
	require.Same(t, first, cached)
	received, err := transport.bind.ParseEndpoint(endpoint.Address())
	require.NoError(t, err)
	source := transport.rememberEndpoint(received)
	cached, err = transport.resolveEndpoint(source)
	require.NoError(t, err)
	require.Same(t, received, cached)
	other := &Transport{bind: conn.NewDefaultBind()}
	_, err = other.resolveEndpoint(source)
	require.ErrorIs(t, err, polyamide.ErrInvalidEndpoint)
	_, err = other.resolveEndpoint(endpoint)
	require.ErrorIs(t, err, polyamide.ErrInvalidEndpoint)
	// Caller-held endpoints survive peer retirement and remain reusable.
	p1, p2 := &peer{owner: transport}, &peer{owner: transport}
	require.NoError(t, transport.SetEndpoints(context.Background(), p1, []polyamide.Endpoint{endpoint}))
	p1.retired.Store(true)
	require.NoError(t, transport.SetEndpoints(context.Background(), p2, []polyamide.Endpoint{endpoint}))
	require.Same(t, first, transport.configuredEndpoints[p2][0])
}
