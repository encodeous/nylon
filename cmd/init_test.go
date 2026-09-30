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

func TestInitServeConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	serverDir, clientDir := t.TempDir(), t.TempDir()
	pr, pw := io.Pipe()
	serve := newInitCmd()
	serve.SetOut(pw)
	serve.SetArgs([]string{"--serve", "--id", "hub", "--port", strconv.Itoa(port),
		"-o", filepath.Join(serverDir, "node.yaml"), "-c", filepath.Join(serverDir, "central.yaml")})
	done := make(chan error, 1)
	go func() {
		done <- serve.Execute()
		_ = pw.Close()
	}()

	var token string
	scanner := bufio.NewScanner(pr)
	for scanner.Scan() {
		if v, ok := strings.CutPrefix(scanner.Text(), "Setup token: "); ok {
			token = v
			break
		}
	}
	require.NotEmpty(t, token)
	go func() { _, _ = io.Copy(io.Discard, pr) }()

	// wrong token must be rejected without consuming the real one
	bad := newInitCmd()
	bad.SetArgs([]string{"--connect", "127.0.0.1:" + strconv.Itoa(port), "--token", "nope", "--id", "evil",
		"-o", filepath.Join(clientDir, "evil.yaml"), "-c", filepath.Join(clientDir, "evil-central.yaml")})
	require.ErrorContains(t, bad.Execute(), "invalid setup token")

	connect := newInitCmd()
	connect.SetOut(io.Discard)
	connect.SetArgs([]string{"--connect", "127.0.0.1:" + strconv.Itoa(port), "--token", token, "--id", "leaf",
		"-o", filepath.Join(clientDir, "node.yaml"), "-c", filepath.Join(clientDir, "central.yaml")})
	require.NoError(t, connect.Execute())
	require.NoError(t, <-done)

	serverCentral, err := os.ReadFile(filepath.Join(serverDir, "central.yaml"))
	require.NoError(t, err)
	clientCentral, err := os.ReadFile(filepath.Join(clientDir, "central.yaml"))
	require.NoError(t, err)
	assert.Equal(t, serverCentral, clientCentral)

	var central state.CentralCfg
	require.NoError(t, yaml.Unmarshal(clientCentral, &central))
	require.NoError(t, state.CentralConfigValidator(&central))
	assert.Equal(t, []string{"127.0.0.1:" + strconv.Itoa(port)}, central.GetRouter("hub").Endpoints)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.1")}, central.GetRouter("hub").Addresses)
	assert.Equal(t, []netip.Addr{netip.MustParseAddr("10.0.0.2")}, central.GetRouter("leaf").Addresses)
	assert.Equal(t, []state.NodeId{"leaf"}, central.GetPeers("hub"))

	for _, dir := range []string{serverDir, clientDir} {
		data, err := os.ReadFile(filepath.Join(dir, "node.yaml"))
		require.NoError(t, err)
		node, err := state.ParseLocalConfig(data)
		require.NoError(t, err)
		require.NoError(t, state.NodeConfigValidator(&central, node))
		assert.Equal(t, central.GetNode(node.Id).PubKey, node.Key.Pubkey())
	}
}
