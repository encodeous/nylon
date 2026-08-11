package cmd

import (
	"bytes"
	"os"
	"path/filepath"
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

func TestInitCommandPromptsForUnsetOptions(t *testing.T) {
	output := filepath.Join(t.TempDir(), "interactive-node.yaml")
	input := strings.Join([]string{
		"INVALID ID",
		"router-interactive",
		"0",
		"60000",
		output,
		"",
		"yes",
		"yes",
		"no",
		"1.1.1.1:53, 8.8.8.8:53",
		"nylon-test",
		"/var/log/nylon.log",
		"no",
		"10.0.0.0/8",
		"192.168.0.0/24",
		"echo pre-up",
		"echo pre-down",
		"echo post-up",
		"echo post-down",
	}, "\n") + "\n"

	cmd := newInitCmd()
	cmd.SetIn(strings.NewReader(input))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	require.NoError(t, cmd.Execute())

	data, err := os.ReadFile(output)
	require.NoError(t, err)
	var cfg state.LocalCfg
	require.NoError(t, yaml.Unmarshal(data, &cfg))
	assert.Equal(t, state.NodeId("router-interactive"), cfg.Id)
	assert.Equal(t, uint16(60000), cfg.Port)
	assert.True(t, cfg.UseSystemRouting)
	assert.False(t, cfg.NoNetConfigure)
	assert.Equal(t, []string{"1.1.1.1:53", "8.8.8.8:53"}, cfg.DnsResolvers)
	assert.Equal(t, "nylon-test", cfg.InterfaceName)
	assert.Equal(t, "/var/log/nylon.log", cfg.LogPath)
	require.Len(t, cfg.UnexcludeIPs, 1)
	assert.Equal(t, "10.0.0.0/8", cfg.UnexcludeIPs[0].String())
	require.Len(t, cfg.ExcludeIPs, 1)
	assert.Equal(t, "192.168.0.0/24", cfg.ExcludeIPs[0].String())
	assert.Equal(t, []string{"echo pre-up"}, cfg.PreUp)
	assert.Equal(t, []string{"echo pre-down"}, cfg.PreDown)
	assert.Equal(t, []string{"echo post-up"}, cfg.PostUp)
	assert.Equal(t, []string{"echo post-down"}, cfg.PostDown)
	assert.Contains(t, stdout.String(), "Interactive node configuration")
	assert.Contains(t, stdout.String(), "Invalid node ID")
	assert.Contains(t, stdout.String(), "Port must be a number between 1 and 65535")
	assert.Contains(t, stdout.String(), "Created "+output)
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

func TestBuildNodeConfigRequiresCompleteDistributionConfig(t *testing.T) {
	_, err := buildNodeConfig(initOptions{
		id:      "router-1",
		port:    57175,
		distURL: "https://example.com/central.nybundle",
	})
	require.ErrorContains(t, err, "--dist-url and --dist-key")
}

func TestBuildNodeConfigRejectsInvalidValues(t *testing.T) {
	_, err := buildNodeConfig(initOptions{id: "INVALID ID", port: 57175})
	require.ErrorContains(t, err, "invalid node config")

	_, err = buildNodeConfig(initOptions{id: "router-1", port: 57175, excludeIPs: []string{"not-a-prefix"}})
	require.ErrorContains(t, err, "invalid --exclude-ip")
}
