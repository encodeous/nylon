package core

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/require"
)

func TestObservabilityHealth(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	n := &Nylon{Context: ctx}

	rec := httptest.NewRecorder()
	n.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "ok\n", rec.Body.String())

	cancel(context.Canceled)
	rec = httptest.NewRecorder()
	n.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestObservabilityDiscovery(t *testing.T) {
	n := &Nylon{ConfigState: state.ConfigState{
		LocalCfg: state.LocalCfg{ObservabilityAddr: "0.0.0.0:9090"},
		CentralCfg: state.CentralCfg{Routers: []state.RouterCfg{{
			NodeCfg: state.NodeCfg{
				Id:        "alice",
				Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("fd00::1")},
			},
		}}},
	}}
	rec := httptest.NewRecorder()
	n.handleDiscovery(rec, httptest.NewRequest(http.MethodGet, "/discovery", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"targets":["10.0.0.1:9090","[fd00::1]:9090"],"labels":{"nylon_node":"alice"}}]`, rec.Body.String())
}

func TestPrometheusMetrics(t *testing.T) {
	status := &protocol.StatusResponse{
		Node: &protocol.NodeStatus{
			ConfigTimestamp: 1731117600 * int64(time.Second),
			Stats:           &protocol.NodeStats{TxBytes: 12},
		},
		Neighbours: []*protocol.NeighbourInfo{
			{
				PeerId: "bob",
				Wireguard: &protocol.WireGuardPeerStats{
					TxBytes:             7,
					LatestHandshakeUnix: 1786226710556904600,
				},
			},
			{PeerId: "eve", Wireguard: &protocol.WireGuardPeerStats{TxBytes: 5}},
		},
	}
	var buf bytes.Buffer
	writePrometheusMetrics(&buf, status)
	output := buf.String()
	require.Contains(t, output, "# TYPE nylon_wireguard_transmit_bytes_total counter")
	require.Contains(t, output, "nylon_config_timestamp_seconds 1731117600\n")
	require.Contains(t, output, `nylon_wireguard_peer_transmit_bytes_total{peer="bob"} 7`)
	require.Contains(t, output, `nylon_wireguard_peer_latest_handshake_seconds{peer="bob"} 1786226710`)
	require.NotContains(t, output, `nylon_wireguard_peer_latest_handshake_seconds{peer="bob"} 1.786`)
	require.Equal(t, 1, strings.Count(output, "# HELP nylon_wireguard_peer_transmit_bytes_total "))
}
