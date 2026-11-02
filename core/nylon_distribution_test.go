package core

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

func testBundle(t *testing.T) (string, state.NyPublicKey) {
	t.Helper()
	key := state.GenerateKey()
	cfg := state.CentralCfg{
		Routers: []state.RouterCfg{{NodeCfg: state.NodeCfg{Id: "a", PubKey: state.GenerateKey().Pubkey()}}},
	}
	raw, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	bundle, err := state.BundleConfig(string(raw), key)
	require.NoError(t, err)
	return bundle, key.Pubkey()
}

// polling a repo must not leave a keep-alive connection open on every fetch
func TestFetchConfigDoesNotLeakConnections(t *testing.T) {
	bundle, pub := testBundle(t)
	var mu sync.Mutex
	open := make(map[net.Conn]struct{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(bundle))
	}))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		switch s {
		case http.StateNew:
			open[c] = struct{}{}
		case http.StateClosed, http.StateHijacked:
			delete(open, c)
		}
	}
	srv.Start()
	defer srv.Close()

	resolver := state.NewDNSResolver(nil)
	for range 20 {
		_, err := fetchConfig(srv.URL, pub, 1<<20, resolver)
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(open) <= 1
	}, time.Second, 10*time.Millisecond, "connections left open after 20 polls")
}

// a repo that accepts the request but never answers must not block the poller forever
func TestFetchConfigTimesOutOnStalledRepo(t *testing.T) {
	_, pub := testBundle(t)
	old := fetchConfigTimeout
	fetchConfigTimeout = 200 * time.Millisecond
	t.Cleanup(func() { fetchConfigTimeout = old })
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)

	done := make(chan error, 1)
	go func() {
		_, err := fetchConfig(srv.URL, pub, 1<<20, state.NewDNSResolver(nil))
		done <- err
	}()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("fetchConfig still blocked on a stalled repo")
	}
}
