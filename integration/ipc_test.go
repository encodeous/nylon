//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/polyamide/device"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/encoding/protojson"
)

func setupTwoNodeHarness(t *testing.T) (*VirtualHarness, chan error) {
	t.Helper()
	vh := &VirtualHarness{}
	a1 := "192.168.50.1:1234"
	vh.NewNode("a", "10.0.0.1/32")
	b1 := "192.168.50.2:1234"
	b2 := "192.168.50.3:1234"
	vh.NewNode("b", "10.0.0.2/32")
	vh.Central.Graph = []string{"a, b"}
	vh.Endpoints = map[string]state.NodeId{a1: "a", b1: "b", b2: "b"}
	vh.AddLink(a1, b1)
	vh.AddLink(b1, a1)
	vh.AddLink(a1, b2)
	vh.AddLink(b2, a1)
	errs := vh.Start()
	return vh, errs
}

func ipcCall(t *testing.T, n *core.Nylon, req *protocol.IpcRequest) *protocol.IpcResponse {
	t.Helper()
	m := protojson.MarshalOptions{EmitUnpopulated: true}
	data, err := m.Marshal(req)
	require.NoError(t, err)
	data = append(data, '\n')

	var outBuf bytes.Buffer
	rw := bufio.NewReadWriter(
		bufio.NewReader(bytes.NewReader(data)),
		bufio.NewWriter(&outBuf),
	)
	err = core.HandleNylonIPC(n, rw)
	require.True(t, errors.Is(err, device.ErrIPCStatusHandled), "unexpected error: %v", err)

	resp := &protocol.IpcResponse{}
	um := protojson.UnmarshalOptions{DiscardUnknown: true}
	require.NoError(t, um.Unmarshal(outBuf.Bytes(), resp))
	return resp
}

func TestIPCStatus(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, errs := setupTwoNodeHarness(t)
	defer vh.Stop()

	// Wait for links to come up
	time.Sleep(3 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")]
	done := make(chan *protocol.IpcResponse, 1)
	a.Dispatch(func() error {
		resp := ipcCall(t, a, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Status{Status: &protocol.StatusRequest{}},
		})
		done <- resp
		return nil
	})

	select {
	case resp := <-done:
		assert.True(t, resp.Ok)
		s := resp.GetStatus()
		require.NotNil(t, s.GetNode())
		assert.Equal(t, "a", s.GetNode().NodeId)
		assert.NotEmpty(t, s.GetNode().PublicKey)
		assert.Equal(t, int32(1), s.GetNode().GetStats().NeighbourCount)
		assert.GreaterOrEqual(t, s.GetNode().GetStats().SelectedRouteCount, int32(1))
		assert.GreaterOrEqual(t, s.GetNode().GetStats().AdvertisedPrefixCount, int32(1))

		require.Len(t, s.GetNeighbours(), 1)
		peer := s.GetNeighbours()[0]
		assert.Equal(t, "b", peer.PeerId)
		assert.NotEmpty(t, peer.PublicKey)
		require.NotEmpty(t, peer.GetEndpoints())
		assert.GreaterOrEqual(t, len(peer.GetEndpoints()), 2)
		assert.NotEmpty(t, peer.GetEndpoints()[0].Address)

		assert.GreaterOrEqual(t, len(s.GetRoutes().GetSelected()), 1)
		assert.GreaterOrEqual(t, len(s.GetRoutes().GetForward()), 1)
		assert.GreaterOrEqual(t, len(s.GetFeasibilityDistances()), 1)
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

func TestIPCReloadConfig(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, errs := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	// Write the current central config to a temp file
	cfgData, err := yaml.Marshal(&vh.Central)
	require.NoError(t, err)
	tmpFile := t.TempDir() + "/central.yaml"
	require.NoError(t, os.WriteFile(tmpFile, cfgData, 0600))

	a := vh.Nylons[vh.IndexOf("a")]
	a.ConfigPath = tmpFile
	done := make(chan *protocol.IpcResponse, 1)
	go func() {
		resp := ipcCall(t, a, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Reload{Reload: &protocol.ReloadRequest{}},
		})
		done <- resp
	}()

	select {
	case resp := <-done:
		assert.True(t, resp.Ok)
		r := resp.GetReload()
		assert.Contains(t, []protocol.ReloadResult{protocol.ReloadResult_APPLIED}, r.Result)
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

func TestIPCProbeNonNeighbour(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, errs := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")]
	done := make(chan *protocol.IpcResponse, 1)
	a.Dispatch(func() error {
		resp := ipcCall(t, a, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: "nonexistent"}},
		})
		done <- resp
		return nil
	})

	select {
	case resp := <-done:
		assert.False(t, resp.Ok)
		assert.Contains(t, resp.Error, "not a neighbour")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

func TestIPCTraceDisabled(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, errs := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")]
	done := make(chan *protocol.IpcResponse, 1)
	a.Dispatch(func() error {
		// Trace should fail since DBG_trace_tc is false by default
		resp := ipcCall(t, a, &protocol.IpcRequest{
			Request: &protocol.IpcRequest_Trace{Trace: &protocol.TraceRequest{}},
		})
		done <- resp
		return nil
	})

	select {
	case resp := <-done:
		assert.False(t, resp.Ok)
		assert.Contains(t, resp.Error, "tracing not enabled")
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

func TestIPCMalformedRequest(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, errs := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")]
	done := make(chan bool, 1)
	a.Dispatch(func() error {
		// Send garbage
		var outBuf bytes.Buffer
		rw := bufio.NewReadWriter(
			bufio.NewReader(bytes.NewReader([]byte("not json\n"))),
			bufio.NewWriter(&outBuf),
		)
		err := core.HandleNylonIPC(a, rw)
		// Should write an error response, not crash
		assert.True(t, errors.Is(err, device.ErrIPCStatusHandled), "unexpected error: %v", err)
		resp := &protocol.IpcResponse{}
		um := protojson.UnmarshalOptions{DiscardUnknown: true}
		assert.NoError(t, um.Unmarshal(outBuf.Bytes(), resp))
		assert.False(t, resp.Ok)
		done <- true
		return nil
	})

	select {
	case <-done:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

func TestIPCSocketResponseHasNoUAPIErrnoTrailer(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, errs := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")]
	client, server := net.Pipe()
	defer client.Close()
	go a.Device.IpcHandle(server)

	m := protojson.MarshalOptions{EmitUnpopulated: true}
	data, err := m.Marshal(&protocol.IpcRequest{
		Request: &protocol.IpcRequest_Status{Status: &protocol.StatusRequest{}},
	})
	require.NoError(t, err)

	_, err = client.Write(append(append([]byte("get=nylon\n"), data...), '\n'))
	require.NoError(t, err)

	reader := bufio.NewReader(client)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err)

	resp := &protocol.IpcResponse{}
	um := protojson.UnmarshalOptions{DiscardUnknown: true}
	require.NoError(t, um.Unmarshal(line, resp))
	require.True(t, resp.Ok)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	extra, err := reader.ReadString('\n')
	require.Error(t, err)
	require.NotContains(t, extra, "errno=")

	select {
	case err := <-errs:
		t.Fatal(err)
	default:
	}
}
