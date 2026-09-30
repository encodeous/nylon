package cmd

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCommandCreatesValidConfig(t *testing.T) {
	output := filepath.Join(t.TempDir(), "node.yaml")
	cmd := newInitCmd()
	cmd.SetIn(strings.NewReader(""))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{
		"--id", "router-1",
		"--output", output,
		"--dns-resolver", "1.1.1.1:53",
		"--exclude-ip", "192.168.0.0/24",
		"--interface-name", "nylon-test",
	})

	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var cfg state.LocalCfg
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	require.NoError(t, state.NodeConfigValidator(nil, &cfg))
	assert.Equal(t, state.NodeId("router-1"), cfg.Id)
	assert.Equal(t, uint16(57175), cfg.Port)
	assert.Equal(t, []string{"1.1.1.1:53"}, cfg.DnsResolvers)
	assert.Equal(t, "nylon-test", cfg.InterfaceName)
	assert.NotContains(t, stdout.String(), "Interactive node configuration")

	info, err := os.Stat(output)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestInitCommandRefusesToOverwrite(t *testing.T) {
	output := filepath.Join(t.TempDir(), "node.yaml")
	require.NoError(t, os.WriteFile(output, []byte("existing"), 0o600))

	cmd := newInitCmd()
	cmd.SetArgs([]string{"--id", "router-1", "--output", output})
	err := cmd.Execute()
	require.ErrorContains(t, err, "already exists")

	data, readErr := os.ReadFile(output)
	require.NoError(t, readErr)
	assert.Equal(t, "existing", string(data))
}

func TestBuildNodeConfigRejectsInvalidValues(t *testing.T) {
	_, err := buildNodeConfig(initOptions{id: "INVALID ID", port: 57175})
	require.ErrorContains(t, err, "invalid node config")

	_, err = buildNodeConfig(initOptions{id: "router-1", port: 57175, excludeIPs: []string{"not-a-prefix"}})
	require.ErrorContains(t, err, "invalid --exclude-ip")
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}

// starts --serve and returns its token and a channel with its result
func startServe(t *testing.T, dir string, port int, args ...string) (string, chan error) {
	pr, pw := io.Pipe()
	serve := newInitCmd()
	serve.SetOut(pw)
	serve.SetArgs(append([]string{"--serve", "--id", "hub", "--port", strconv.Itoa(port),
		"-o", filepath.Join(dir, "node.yaml"), "-c", filepath.Join(dir, "central.yaml")}, args...))
	done := make(chan error, 1)
	go func() {
		err := serve.Execute()
		_ = pw.Close()
		done <- err
	}()
	var token string
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(scanner.Text(), "Setup token: "); ok {
			token = v
			break
		}
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	return token, done
}

func connect(dir string, port int, token, id string, args ...string) error {
	cmd := newInitCmd()
	cmd.SetOut(io.Discard)
	cmd.SetArgs(append([]string{"--connect", "127.0.0.1:" + strconv.Itoa(port), "--token", token, "--id", id,
		"-o", filepath.Join(dir, "node.yaml"), "-c", filepath.Join(dir, "central.yaml")}, args...))
	return cmd.Execute()
}

func readCentral(t *testing.T, path string) (state.CentralCfg, []byte) {
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var central state.CentralCfg
	require.NoError(t, yaml.Unmarshal(data, &central))
	require.NoError(t, state.CentralConfigValidator(&central))
	return central, data
}

func TestInitServeConnect(t *testing.T) {
	port := freePort(t)
	serverDir, leafDir, leaf2Dir := t.TempDir(), t.TempDir(), t.TempDir()
	serverCentral := filepath.Join(serverDir, "central.yaml")
	joinCentral := filepath.Join(serverDir, "central.join.yaml")

	// new network writes central.yaml directly
	token, done := startServe(t, serverDir, port, "--address", "10.0.0.1")
	require.NotEmpty(t, token)
	// rejected joins must not consume the token
	require.ErrorContains(t, connect(t.TempDir(), port, "nope", "evil"), "invalid setup token")
	require.ErrorContains(t, connect(t.TempDir(), port, token, "dup", "--address", "10.0.0.1"), "already used by hub")
	require.NoError(t, connect(leafDir, port, token, "leaf", "--address", "10.0.0.2", "--prefix", "192.168.5.0/24"))
	require.NoError(t, <-done)

	central, serverData := readCentral(t, serverCentral)
	_, leafData := readCentral(t, filepath.Join(leafDir, "central.yaml"))
	assert.Equal(t, serverData, leafData)
	assert.NoFileExists(t, joinCentral)
	assert.Equal(t, []string{"127.0.0.1:" + strconv.Itoa(port)}, central.GetRouter("hub").Endpoints)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.1")}, central.GetRouter("hub").Addresses)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.2")}, central.GetRouter("leaf").Addresses)
	require.Len(t, central.GetRouter("leaf").Prefixes, 1)
	assert.Equal(t, netip.MustParsePrefix("192.168.5.0/24"), central.GetRouter("leaf").Prefixes[0].GetPrefix())
	assert.Empty(t, central.Graph)
	for _, dir := range []string{serverDir, leafDir} {
		data, err := os.ReadFile(filepath.Join(dir, "node.yaml"))
		require.NoError(t, err)
		node, err := state.ParseLocalConfig(data)
		require.NoError(t, err)
		require.NoError(t, state.NodeConfigValidator(&central, node))
		assert.Equal(t, central.GetNode(node.Id).PubKey, node.Key.Pubkey())
	}

	// existing network keeps central.yaml and writes the join beside it
	token, done = startServe(t, serverDir, port)
	require.NoError(t, connect(leaf2Dir, port, token, "leaf2", "--address", "10.0.0.3"))
	require.NoError(t, <-done)

	_, after := readCentral(t, serverCentral)
	assert.Equal(t, serverData, after)
	joined, joinData := readCentral(t, joinCentral)
	_, leaf2Data := readCentral(t, filepath.Join(leaf2Dir, "central.yaml"))
	assert.Equal(t, joinData, leaf2Data)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.3")}, joined.GetRouter("leaf2").Addresses)
	assert.Empty(t, joined.Graph)

	// an undistributed join must not be overwritten
	serve := newInitCmd()
	serve.SetArgs([]string{"--serve", "--port", strconv.Itoa(freePort(t)),
		"-o", filepath.Join(serverDir, "node.yaml"), "-c", serverCentral})
	require.ErrorContains(t, serve.Execute(), "undistributed join for leaf2")

	// once distributed a new join is allowed again
	require.NoError(t, os.WriteFile(serverCentral, joinData, 0o600))
	token, done = startServe(t, serverDir, port)
	require.NoError(t, connect(t.TempDir(), port, token, "leaf3"))
	require.NoError(t, <-done)
}
