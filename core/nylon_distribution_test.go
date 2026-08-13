package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testConfigURL = "https://config.example/config.nybundle"

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testConfigFetcher(handler http.Handler) *configFetcher {
	fetcher := newConfigFetcher(state.NewDNSResolver(nil))
	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder.Result(), nil
	})
	return fetcher
}

func testConfigBundle(t *testing.T) (string, state.NyPrivateKey) {
	t.Helper()
	key := state.GenerateKey()
	cfg := state.CentralCfg{
		Routers: []state.RouterCfg{{
			NodeCfg: state.NodeCfg{Id: "node"},
		}},
		Graph: []string{"node, node"},
	}
	data, err := yaml.Marshal(cfg)
	require.NoError(t, err)
	bundle, err := state.BundleConfig(string(data), key)
	require.NoError(t, err)
	return bundle, key
}

func TestConfigFetcherUsesETagAndHandlesNotModified(t *testing.T) {
	bundle, key := testConfigBundle(t)
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", `"config-v1"`)
		if r.Header.Get("If-None-Match") == `"config-v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		assert.Empty(t, r.Header.Get("If-None-Match"))
		_, _ = fmt.Fprint(w, bundle)
	})

	fetcher := testConfigFetcher(handler)
	first, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	require.NotNil(t, first.config)
	assert.False(t, first.notModified)

	second, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	assert.Nil(t, second.config)
	assert.True(t, second.notModified)
	assert.Equal(t, int32(2), requests.Load())
}

func TestConfigFetcherUsesLastModified(t *testing.T) {
	bundle, key := testConfigBundle(t)
	lastModified := time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat)
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Last-Modified", lastModified)
		if r.Header.Get("If-Modified-Since") == lastModified {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = fmt.Fprint(w, bundle)
	})

	fetcher := testConfigFetcher(handler)
	_, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	result, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	assert.True(t, result.notModified)
	assert.Equal(t, int32(2), requests.Load())
}

func TestConfigFetcherHonorsFreshnessLifetime(t *testing.T) {
	bundle, key := testConfigBundle(t)
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Cache-Control", "max-age=60")
		_, _ = fmt.Fprint(w, bundle)
	})

	fetcher := testConfigFetcher(handler)
	_, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	result, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	assert.True(t, result.notModified)
	assert.Equal(t, int32(1), requests.Load(), "a fresh cached response should avoid a network request")
}

func TestConfigFreshnessHonorsExpiresAndAge(t *testing.T) {
	now := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	entry := &configCacheEntry{
		date:    now.Add(-10 * time.Second).Format(http.TimeFormat),
		expires: now.Add(50 * time.Second).Format(http.TimeFormat),
		age:     "20",
	}

	cacheable, freshUntil := configFreshness(entry, now)
	assert.True(t, cacheable)
	assert.Equal(t, now.Add(40*time.Second), freshUntil)
}

func TestConfigFetcherHonorsNoStore(t *testing.T) {
	bundle, key := testConfigBundle(t)
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		assert.Empty(t, r.Header.Get("If-None-Match"))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("ETag", `"config-v1"`)
		_, _ = fmt.Fprint(w, bundle)
	})

	fetcher := testConfigFetcher(handler)
	_, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	result, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	assert.NotNil(t, result.config)
	assert.Equal(t, int32(2), requests.Load())
}

func TestConfigFetcherRejectsNonSuccessfulStatus(t *testing.T) {
	bundle, key := testConfigBundle(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, bundle)
	})

	fetcher := testConfigFetcher(handler)
	_, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	assert.ErrorContains(t, err, "unexpected HTTP status 500 Internal Server Error")
}

func TestConfigFetcherDoesNotCacheInvalidBundleValidator(t *testing.T) {
	bundle, key := testConfigBundle(t)
	var requests atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		assert.Empty(t, r.Header.Get("If-None-Match"))
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("ETag", `"config-v1"`)
		if request == 1 {
			_, _ = fmt.Fprint(w, "not a valid bundle")
			return
		}
		_, _ = fmt.Fprint(w, bundle)
	})

	fetcher := testConfigFetcher(handler)
	_, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.Error(t, err)
	result, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 1<<20)
	require.NoError(t, err)
	assert.NotNil(t, result.config)
	assert.Equal(t, int32(2), requests.Load())
}

func TestConfigFetcherRejectsOversizedResponse(t *testing.T) {
	_, key := testConfigBundle(t)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "12345")
	})

	fetcher := testConfigFetcher(handler)
	_, err := fetcher.fetch(context.Background(), testConfigURL, key.Pubkey(), 4)
	assert.ErrorContains(t, err, "config exceeds maximum size of 4 bytes")
}

func TestUpdateConfigPollDelay(t *testing.T) {
	tunables := state.DefaultRouterTunables()
	n := &Nylon{RouterTunables: tunables}
	n.updateConfigPollDelay(nil)
	assert.Equal(t, 10*time.Second, time.Duration(n.configPollDelay.Load()))

	interval := 45 * time.Second
	n.updateConfigPollDelay(&state.CentralCfg{Dist: &state.DistributionCfg{PollInterval: &interval}})
	assert.Equal(t, interval, time.Duration(n.configPollDelay.Load()))
}
