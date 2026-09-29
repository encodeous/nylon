package core

import (
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"testing"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSystemRoutes struct {
	addresses  []netip.Prefix
	routes     []netip.Prefix
	addrsRead  error
	routesRead error
}

func (f *fakeSystemRoutes) InterfaceAddresses(string) ([]netip.Prefix, error) {
	return slices.Clone(f.addresses), f.addrsRead
}

func (f *fakeSystemRoutes) AddAddress(_ string, addr netip.Addr) error {
	f.addresses = append(f.addresses, state.AddrToPrefix(addr))
	return nil
}

func (f *fakeSystemRoutes) DeleteAddress(_ string, addr netip.Addr) error {
	f.addresses = slices.DeleteFunc(f.addresses, func(prefix netip.Prefix) bool {
		return prefix.Addr() == addr
	})
	return nil
}

func (f *fakeSystemRoutes) InterfaceRoutes(string) ([]netip.Prefix, error) {
	return slices.Clone(f.routes), f.routesRead
}

func (f *fakeSystemRoutes) AddRoute(_ string, prefix netip.Prefix) error {
	f.routes = append(f.routes, prefix)
	return nil
}

func (f *fakeSystemRoutes) DeleteRoute(_ string, prefix netip.Prefix) error {
	f.routes = slices.Delete(f.routes, slices.Index(f.routes, prefix), slices.Index(f.routes, prefix)+1)
	return nil
}

func TestSyncSystemStateReconcilesAgainstLiveState(t *testing.T) {
	desiredAddress := netip.MustParseAddr("10.0.0.1")
	desiredRoute := netip.MustParsePrefix("10.1.0.0/16")
	externalAddress := netip.MustParsePrefix("10.0.0.2/32")
	externalRoute := netip.MustParsePrefix("10.2.0.0/16")
	system := &fakeSystemRoutes{
		addresses: []netip.Prefix{externalAddress},
		routes:    []netip.Prefix{externalRoute},
	}
	n := &Nylon{
		ConfigState: state.ConfigState{
			CentralCfg: state.CentralCfg{Routers: []state.RouterCfg{{
				NodeCfg: state.NodeCfg{Id: "a", Addresses: []netip.Addr{desiredAddress}},
			}}},
			LocalCfg: state.LocalCfg{Id: "a"},
		},
		RouterState: &state.RouterState{
			Routes: map[netip.Prefix]state.SelRoute{desiredRoute: {Nh: "b"}},
		},
		Interface:    "nylon0",
		SystemRoutes: system,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	require.NoError(t, n.SyncSystemState())
	assert.Equal(t, []netip.Prefix{state.AddrToPrefix(desiredAddress)}, system.addresses)
	assert.Equal(t, []netip.Prefix{desiredRoute}, system.routes)

	// Simulate another process removing Nylon's state between reconciliations.
	system.addresses = nil
	system.routes = nil
	require.NoError(t, n.SyncSystemState())
	assert.Equal(t, []netip.Prefix{state.AddrToPrefix(desiredAddress)}, system.addresses)
	assert.Equal(t, []netip.Prefix{desiredRoute}, system.routes)
}

func TestSyncSystemStateDoesNotMutateAfterReadFailure(t *testing.T) {
	readErr := errors.New("route query failed")
	system := &fakeSystemRoutes{routesRead: readErr}
	n := &Nylon{
		ConfigState: state.ConfigState{
			CentralCfg: state.CentralCfg{Routers: []state.RouterCfg{{
				NodeCfg: state.NodeCfg{Id: "a"},
			}}},
			LocalCfg: state.LocalCfg{Id: "a"},
		},
		RouterState: &state.RouterState{
			Routes: map[netip.Prefix]state.SelRoute{
				netip.MustParsePrefix("10.1.0.0/16"): {Nh: "b"},
			},
		},
		Interface:    "nylon0",
		SystemRoutes: system,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	err := n.SyncSystemState()
	require.ErrorIs(t, err, readErr)
	assert.Empty(t, system.routes)
}
