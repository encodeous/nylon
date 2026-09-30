package adapter

import (
	"testing"

	"github.com/encodeous/nylon/polyamide/transports/wireguard/tun"
	"github.com/stretchr/testify/require"
)

func TestHostSinkReportsMTUChanges(t *testing.T) {
	h := newHostSink(1)
	h.setMTU(1280)
	mtu, err := h.MTU()
	require.NoError(t, err)
	require.Equal(t, 1280, mtu)
	require.Equal(t, tun.Event(tun.EventMTUUpdate), <-h.events)
	h.setMTU(1280)
	require.Empty(t, h.events)
	require.NoError(t, h.Close())
	h.setMTU(1500) // must not send on the closed channel
}
