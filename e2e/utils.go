package e2e

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

// SetupTestDir creates a directory for the current test run
func (h *Harness) SetupTestDir() string {
	dir := filepath.Join(h.RootDir, "e2e", "runs", h.t.Name())
	// Clean up previous run
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		h.t.Fatal(err)
	}
	return dir
}

// WriteConfig marshals the config to YAML and writes it to the specified directory with the given filename
func (h *Harness) WriteConfig(dir, filename string, cfg any) string {
	path := filepath.Join(dir, filename)
	data, err := yaml.Marshal(cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

// SimpleRouter creates a basic RouterCfg with the given parameters
func SimpleRouter(id string, pubKey state.NyPublicKey, nylonIP string, endpointIP string) state.RouterCfg {
	cfg := state.RouterCfg{
		NodeCfg: state.NodeCfg{
			Id:     state.NodeId(id),
			PubKey: pubKey,
			Addresses: []netip.Addr{
				netip.MustParseAddr(nylonIP),
			},
		},
	}
	if endpointIP != "" {
		cfg.Endpoints = []netip.AddrPort{
			netip.MustParseAddrPort(fmt.Sprintf("%s:57175", endpointIP)),
		}
	}
	return cfg
}

// SimpleLocal creates a basic LocalCfg with the given parameters and defaults
func SimpleLocal(id string, key state.NyPrivateKey) state.LocalCfg {
	return state.LocalCfg{
		Id:             state.NodeId(id),
		Key:            key,
		Port:           57175,
		NoNetConfigure: false,
		InterfaceName:  "nylon0",
	}
}
