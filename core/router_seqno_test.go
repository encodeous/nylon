package core

import (
	"net/netip"
	"testing"

	"github.com/encodeous/nylon/state"
	"github.com/stretchr/testify/assert"
)

func TestRequestSeqnoDedupAcrossWraparound(t *testing.T) {
	n := &Nylon{RouterTunables: state.DefaultRouterTunables()}
	n.router.IO = make(map[state.NodeId]*IOPending)
	src := state.Source{NodeId: "s", Prefix: netip.MustParsePrefix("10.0.0.19/32")}
	flush := func() int {
		nio := n.GetNeighIO("b")
		sent := len(nio.SeqnoReq)
		clear(nio.SeqnoReq)
		return sent
	}

	// without wraparound a repeated request is deduplicated
	n.RequestSeqno("b", src, 100, 64)
	assert.Equal(t, 1, flush())
	n.RequestSeqno("b", src, 100, 64)
	assert.Equal(t, 0, flush())

	// 65535 then 0 so the second request is newer and goes out
	src = state.Source{NodeId: "s", Prefix: netip.MustParsePrefix("10.0.0.20/32")}
	n.RequestSeqno("b", src, 65535, 64)
	assert.Equal(t, 1, flush())
	n.RequestSeqno("b", src, 0, 64)
	assert.Equal(t, 1, flush())

	// repeating the request for 0 must be deduplicated
	for range 5 {
		n.RequestSeqno("b", src, 0, 64)
		assert.Equal(t, 0, flush(), "duplicate seqno request for 0 was not suppressed")
	}
}
