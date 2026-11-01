//go:build integration

package integration

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/encodeous/nylon/core"
	"github.com/encodeous/nylon/ipc"
	"github.com/encodeous/nylon/protocol"
	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
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

// ipcClient connects a client to n's IPC server over an in-memory pipe.
func ipcClient(t *testing.T, n *core.Nylon) *ipc.Client {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = core.ServeIPC(n, server)
	}()
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})
	c, err := ipc.NewClient(client)
	require.NoError(t, err)
	return c
}

func ipcCall(t *testing.T, n *core.Nylon, req *protocol.IpcRequest) (*protocol.IpcResponse, error) {
	t.Helper()
	return ipcClient(t, n).Do(req)
}

// ipcErrorCode is the code of the daemon's error, failing the test for any other kind of error.
func ipcErrorCode(t *testing.T, err error) protocol.IpcError_Code {
	t.Helper()
	var ipcErr *ipc.Error
	require.True(t, errors.As(err, &ipcErr), "expected a daemon error, got %v", err)
	return ipcErr.Code
}

func TestIPCStatus(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, _ := setupTwoNodeHarness(t)
	defer vh.Stop()

	// Wait for links to come up
	time.Sleep(3 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")].Load()
	resp, err := ipcCall(t, a, &protocol.IpcRequest{
		Request: &protocol.IpcRequest_Status{Status: &protocol.StatusRequest{}},
	})
	require.NoError(t, err)
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
}

func TestIPCProbeReportsTimeout(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh := &VirtualHarness{}
	a1 := "192.168.52.1:1234"
	b1 := "192.168.52.2:1234"
	vh.NewNode("a", "10.0.0.1/32")
	vh.NewNode("b", "10.0.0.2/32")
	vh.Central.Graph = []string{"a, b"}
	vh.Endpoints = map[string]state.NodeId{a1: "a", b1: "b"}
	vh.AddLink(a1, b1).WithPacketLoss(1)
	vh.AddLink(b1, a1).WithPacketLoss(1)
	errs := vh.Start()
	defer vh.Stop()

	time.Sleep(500 * time.Millisecond)

	a := vh.Nylons[vh.IndexOf("a")].Load()
	resp, err := ipcCall(t, a, &protocol.IpcRequest{
		Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: "b", TimeoutMs: 100}},
	})
	require.NoError(t, err)
	results := resp.GetProbe().GetResults()
	require.NotEmpty(t, results)
	for _, result := range results {
		assert.Equal(t, protocol.EndpointProbeStatus_ENDPOINT_PROBE_TIMEOUT, result.Status)
		assert.Zero(t, result.LatencyNs)
	}

	select {
	case err := <-errs:
		t.Fatal(err)
	default:
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

	a := vh.Nylons[vh.IndexOf("a")].Load()
	a.ConfigPath = tmpFile
	client := ipcClient(t, a)
	type result struct {
		resp *protocol.IpcResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Do(&protocol.IpcRequest{
			Request: &protocol.IpcRequest_Reload{Reload: &protocol.ReloadRequest{}},
		})
		done <- result{resp, err}
	}()

	select {
	case res := <-done:
		require.NoError(t, res.err)
		r := res.resp.GetReload()
		assert.Equal(t, protocol.ReloadResult_NOOP, r.Result)
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout")
	}
}

func TestIPCProbeNonNeighbour(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, _ := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")].Load()
	_, err := ipcCall(t, a, &protocol.IpcRequest{
		Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: "nonexistent"}},
	})
	assert.Equal(t, protocol.IpcError_NOT_FOUND, ipcErrorCode(t, err))
	assert.Contains(t, err.Error(), "not a neighbour")
}

func TestIPCTraceDisabled(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, _ := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")].Load()
	// DBG_trace_tc is false by default
	_, err := ipcCall(t, a, &protocol.IpcRequest{
		Request: &protocol.IpcRequest_Trace{Trace: &protocol.TraceRequest{}},
	})
	assert.Equal(t, protocol.IpcError_FAILED_PRECONDITION, ipcErrorCode(t, err))
	assert.Contains(t, err.Error(), "tracing not enabled")
}

// A frame that isn't an IpcRequest gets an error response, then the server hangs up.
func TestIPCMalformedRequest(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, _ := setupTwoNodeHarness(t)
	defer vh.Stop()

	time.Sleep(1 * time.Second)

	a := vh.Nylons[vh.IndexOf("a")].Load()
	client, server := net.Pipe()
	defer client.Close()
	go core.ServeIPC(a, server)

	require.NoError(t, ipc.WriteHello(client))
	version, err := ipc.ReadHello(client)
	require.NoError(t, err)
	require.Equal(t, ipc.Version, version)

	garbage := []byte{0xff, 0xff, 0xff}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(garbage)))
	_, err = client.Write(append(frame, garbage...))
	require.NoError(t, err)

	resp := &protocol.IpcResponse{}
	require.NoError(t, ipc.ReadFrame(client, resp))
	assert.Equal(t, protocol.IpcError_INVALID_REQUEST, resp.GetError().GetCode())
	_, err = client.Read(make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF)
}

// A client with another protocol version learns the server's, and the server hangs up.
func TestIPCVersionMismatch(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh, _ := setupTwoNodeHarness(t)
	defer vh.Stop()

	a := vh.Nylons[vh.IndexOf("a")].Load()
	client, server := net.Pipe()
	defer client.Close()
	served := make(chan error, 1)
	go func() { served <- core.ServeIPC(a, server) }()

	_, err := client.Write([]byte{'N', 'Y', 'L', 'N', 0xff, 0xff})
	require.NoError(t, err)
	version, err := ipc.ReadHello(client)
	require.NoError(t, err)
	assert.Equal(t, ipc.Version, version)
	assert.ErrorAs(t, <-served, &ipc.ErrVersion{})
}

// Requests on one connection run concurrently, and cancelling one ends it early.
func TestIPCConcurrentRequestsAndCancel(t *testing.T) {
	defer goleak.VerifyNone(t)
	vh := &VirtualHarness{}
	a1 := "192.168.53.1:1234"
	b1 := "192.168.53.2:1234"
	vh.NewNode("a", "10.0.0.1/32")
	vh.NewNode("b", "10.0.0.2/32")
	vh.Central.Graph = []string{"a, b"}
	vh.Endpoints = map[string]state.NodeId{a1: "a", b1: "b"}
	vh.AddLink(a1, b1).WithPacketLoss(1)
	vh.AddLink(b1, a1).WithPacketLoss(1)
	vh.Start()
	defer vh.Stop()

	time.Sleep(500 * time.Millisecond)

	a := vh.Nylons[vh.IndexOf("a")].Load()
	client, server := net.Pipe()
	defer client.Close()
	go core.ServeIPC(a, server)
	require.NoError(t, ipc.WriteHello(client))
	_, err := ipc.ReadHello(client)
	require.NoError(t, err)
	require.NoError(t, client.SetDeadline(time.Now().Add(10*time.Second)))

	var writeMu sync.Mutex
	send := func(req *protocol.IpcRequest) {
		writeMu.Lock()
		defer writeMu.Unlock()
		require.NoError(t, ipc.WriteFrame(client, req))
	}
	read := func() *protocol.IpcResponse {
		resp := &protocol.IpcResponse{}
		require.NoError(t, ipc.ReadFrame(client, resp))
		return resp
	}
	// Every packet is lost, so the probe waits out its whole timeout.
	start := time.Now()
	send(&protocol.IpcRequest{Id: 1, Request: &protocol.IpcRequest_Probe{Probe: &protocol.ProbeRequest{PeerId: "b", TimeoutMs: 5000}}})
	send(&protocol.IpcRequest{Id: 2, Request: &protocol.IpcRequest_Status{Status: &protocol.StatusRequest{}}})
	resp := read()
	require.Equal(t, uint32(2), resp.Id, "status should not wait for the probe")
	require.Equal(t, "a", resp.GetStatus().GetNode().GetNodeId())

	send(&protocol.IpcRequest{Request: &protocol.IpcRequest_Cancel{Cancel: &protocol.CancelRequest{Id: 1}}})
	resp = read()
	require.Equal(t, uint32(1), resp.Id)
	require.Less(t, time.Since(start), 4*time.Second, "the cancelled probe should end before its timeout")
}
