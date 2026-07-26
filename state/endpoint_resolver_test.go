package state

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEndpointResolverSharesResolutionByAddress(t *testing.T) {
	dns := &countingDNSResolver{}
	resolver := newEndpointResolver(dns)

	const workers = 8
	type resolveResult struct {
		addr netip.AddrPort
		err  error
	}
	results := make(chan resolveResult, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr, err := resolver.Resolve("router.example.com:1234", time.Hour)
			results <- resolveResult{addr: addr, err: err}
		}()
	}
	wg.Wait()
	close(results)

	expected := netip.MustParseAddrPort("192.0.2.1:1234")
	for result := range results {
		require.NoError(t, result.err)
		assert.Equal(t, expected, result.addr)
	}
	assert.Equal(t, 1, dns.NameCalls())

	resolver.Expire("router.example.com:1234")
	result, err := resolver.Resolve("router.example.com:1234", time.Hour)
	require.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.Equal(t, 2, dns.NameCalls())
}

func TestEndpointResolverDoesNotCacheLiteralAddrPort(t *testing.T) {
	dns := &countingDNSResolver{}
	resolver := newEndpointResolver(dns)

	expected := netip.MustParseAddrPort("[2001:db8::1]:57175")
	result, err := resolver.Resolve(expected.String(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, expected, result)

	result, err = resolver.Get(expected.String())
	require.NoError(t, err)
	assert.Equal(t, expected, result)
	assert.Zero(t, dns.NameCalls())
}

func TestEndpointResolverPrunesUnusedAddresses(t *testing.T) {
	resolver := newEndpointResolver(&countingDNSResolver{})
	const address = "router.example.com:1234"

	_, err := resolver.Resolve(address, time.Hour)
	require.NoError(t, err)
	resolver.Retain(map[string]struct{}{})

	_, err = resolver.Get(address)
	assert.ErrorContains(t, err, "not resolved")
}

func TestEndpointResolverGetDoesNotWaitForRefresh(t *testing.T) {
	dns := &blockingDNSResolver{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	resolver := newEndpointResolver(dns)
	const endpoint = "router.example.com:1234"

	expected, err := resolver.Resolve(endpoint, time.Hour)
	require.NoError(t, err)
	resolver.Expire(endpoint)
	dns.block = true
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			close(dns.release)
		})
	}
	t.Cleanup(release)

	refreshDone := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(endpoint, time.Hour)
		refreshDone <- err
	}()
	<-dns.started

	type getResult struct {
		addr netip.AddrPort
		err  error
	}
	getDone := make(chan getResult, 1)
	go func() {
		addr, err := resolver.Get(endpoint)
		getDone <- getResult{addr: addr, err: err}
	}()

	select {
	case result := <-getDone:
		require.NoError(t, result.err)
		assert.Equal(t, expected, result.addr)
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Get blocked behind an in-flight DNS refresh")
	}

	release()
	require.NoError(t, <-refreshDone)
}

type countingDNSResolver struct {
	mu        sync.Mutex
	nameCalls int
}

func (r *countingDNSResolver) ResolveName(context.Context, string) ([]netip.Addr, error) {
	r.mu.Lock()
	r.nameCalls++
	r.mu.Unlock()
	return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
}

func (*countingDNSResolver) ResolveSRV(context.Context, string, string, string) (string, uint16, error) {
	return "", 0, errors.New("no SRV record")
}

func (r *countingDNSResolver) NameCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nameCalls
}

type blockingDNSResolver struct {
	block   bool
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingDNSResolver) ResolveName(context.Context, string) ([]netip.Addr, error) {
	if r.block {
		r.once.Do(func() {
			close(r.started)
		})
		<-r.release
	}
	return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
}

func (*blockingDNSResolver) ResolveSRV(context.Context, string, string, string) (string, uint16, error) {
	return "", 0, errors.New("no SRV record")
}
