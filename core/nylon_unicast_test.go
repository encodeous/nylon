package core

import (
	"net/netip"
	"testing"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

func TestNyUnicastHeaderRoundTrip(t *testing.T) {
	buf := make([]byte, NyUnicastHeaderSize)
	writeNyUnicastHeader(buf, nyUnicastHeader{
		subtype:  NyUnicastSubtypeExit,
		hopLimit: 42,
		dst:      state.NodeIdBin(0x1234),
		src:      state.NodeIdBin(0x00FE),
	})

	h, err := parseNyUnicastHeader(buf)
	assert.NoError(t, err)
	assert.Equal(t, NyUnicastSubtypeExit, h.subtype)
	assert.EqualValues(t, 42, h.hopLimit)
	assert.EqualValues(t, 0x1234, h.dst)
	assert.EqualValues(t, 0x00FE, h.src)
}

func TestNyUnicastHeaderShortRejected(t *testing.T) {
	_, err := parseNyUnicastHeader(make([]byte, NyUnicastHeaderSize-1))
	assert.Error(t, err)
}

func TestNodeOwnsExitSourceAddrOnlyAllowsAssignedAddresses(t *testing.T) {
	originBin := state.NodeIdBin(1)
	addr := netip.MustParseAddr("10.0.0.1")
	other := netip.MustParseAddr("10.0.0.99")

	snap := &ExitFilterSnapshot{
		NodeAddrs: map[state.NodeIdBin]map[netip.Addr]struct{}{
			originBin: {addr: {}},
		},
	}

	assert.True(t, nodeOwnsExitSourceAddr(snap, originBin, addr))
	assert.False(t, nodeOwnsExitSourceAddr(snap, originBin, other))
	assert.False(t, nodeOwnsExitSourceAddr(snap, state.NodeIdBin(2), addr))
}
