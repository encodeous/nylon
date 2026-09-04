package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

const configFetchTimeout = 30 * time.Second

type configFetchResult struct {
	config      *state.CentralCfg
	notModified bool
}

type configCacheKey struct {
	repo string
	key  state.NyPublicKey
}

type configCacheEntry struct {
	mu sync.Mutex

	valid        bool
	etag         string
	lastModified string
	cacheControl string
	pragma       string
	expires      string
	date         string
	age          string
	vary         string
	freshUntil   time.Time
}

type configFetcher struct {
	client *http.Client
	now    func() time.Time

	mu    sync.Mutex
	cache map[configCacheKey]*configCacheEntry
}

func newConfigFetcher(resolver *state.DNSResolver) *configFetcher {
	if resolver == nil {
		resolver = state.NewDNSResolver(nil)
	}
	transport := new(http.Transport)
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	// Config repository lookups must use Nylon's configured DNS resolver.
	// Preserve the previous no-proxy behavior so a proxy cannot bypass it.
	transport.Proxy = nil
	transport.DialContext = resolver.DialContext
	return &configFetcher{
		client: &http.Client{
			Transport: transport,
			Timeout:   configFetchTimeout,
		},
		now:   time.Now,
		cache: make(map[configCacheKey]*configCacheEntry),
	}
}

func (f *configFetcher) cacheEntry(repo string, key state.NyPublicKey) *configCacheEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	cacheKey := configCacheKey{repo: repo, key: key}
	entry := f.cache[cacheKey]
	if entry == nil {
		entry = new(configCacheEntry)
		f.cache[cacheKey] = entry
	}
	return entry
}

// FetchConfig fetches and verifies a bundled central config.
func FetchConfig(repoStr string, key state.NyPublicKey, maxSize int64) (*state.CentralCfg, error) {
	return fetchConfig(repoStr, key, maxSize, state.NewDNSResolver(nil))
}

// fetchConfig is used for one-shot fetches such as initial bootstrap. Runtime
// polling uses the persistent fetcher on Nylon so validators survive each poll.
func fetchConfig(repoStr string, key state.NyPublicKey, maxSize int64, resolver *state.DNSResolver) (*state.CentralCfg, error) {
	fetcher := newConfigFetcher(resolver)
	defer fetcher.client.CloseIdleConnections()
	result, err := fetcher.fetch(context.Background(), repoStr, key, maxSize)
	if err != nil {
		return nil, err
	}
	if result.notModified || result.config == nil {
		return nil, fmt.Errorf("repository %s returned not modified without a cached config", repoStr)
	}
	return result.config, nil
}

func (f *configFetcher) fetch(ctx context.Context, repoStr string, key state.NyPublicKey, maxSize int64) (configFetchResult, error) {
	if maxSize <= 0 {
		return configFetchResult{}, fmt.Errorf("maximum config size must be greater than 0")
	}
	repo, err := url.Parse(repoStr)
	if err != nil {
		return configFetchResult{}, fmt.Errorf("failed to parse repo URL %s: %w", repoStr, err)
	}

	switch repo.Scheme {
	case "file":
		filePath := repo.Opaque
		if filePath == "" {
			filePath = repo.Path
		}
		file, err := os.Open(filePath)
		if err != nil {
			return configFetchResult{}, fmt.Errorf("failed to read file %s: %w", filePath, err)
		}
		defer file.Close()
		body, err := readLimitedConfig(file, maxSize)
		if err != nil {
			return configFetchResult{}, fmt.Errorf("failed to read file %s: %w", filePath, err)
		}
		config, err := unbundleFetchedConfig(repoStr, body, key)
		return configFetchResult{config: config}, err
	case "http", "https":
		return f.fetchHTTP(ctx, repo.String(), key, maxSize)
	default:
		return configFetchResult{}, fmt.Errorf("unsupported config repository scheme %q", repo.Scheme)
	}
}

func (f *configFetcher) fetchHTTP(ctx context.Context, repo string, key state.NyPublicKey, maxSize int64) (configFetchResult, error) {
	entry := f.cacheEntry(repo, key)
	if !entry.mu.TryLock() {
		// A previous poll of this repository is still in flight. It will publish
		// any update it finds, so starting another identical request adds no value.
		return configFetchResult{notModified: true}, nil
	}
	defer entry.mu.Unlock()

	now := f.now()
	if entry.valid && now.Before(entry.freshUntil) {
		return configFetchResult{notModified: true}, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repo, nil)
	if err != nil {
		return configFetchResult{}, fmt.Errorf("failed to create request for %s: %w", repo, err)
	}
	if entry.valid {
		if entry.etag != "" {
			req.Header.Set("If-None-Match", entry.etag)
		}
		if entry.lastModified != "" {
			req.Header.Set("If-Modified-Since", entry.lastModified)
		}
	}
	conditional := req.Header.Get("If-None-Match") != "" || req.Header.Get("If-Modified-Since") != ""

	res, err := f.client.Do(req)
	if err != nil {
		return configFetchResult{}, fmt.Errorf("failed to fetch %s: %w", repo, err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusNotModified:
		if !entry.valid || !conditional {
			return configFetchResult{}, fmt.Errorf("repository %s returned 304 without a conditional request", repo)
		}
		entry.update(res.Header, true, f.now())
		return configFetchResult{notModified: true}, nil
	case http.StatusOK:
		// Continue below.
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
		return configFetchResult{}, fmt.Errorf("failed to fetch %s: unexpected HTTP status %s", repo, res.Status)
	}

	body, err := readLimitedConfig(res.Body, maxSize)
	if err != nil {
		return configFetchResult{}, fmt.Errorf("failed to read response from %s: %w", repo, err)
	}
	config, err := unbundleFetchedConfig(repo, body, key)
	if err != nil {
		return configFetchResult{}, err
	}

	// Only cache validators after the body has passed cryptographic verification.
	// Otherwise an invalid response with a stable ETag could become permanently
	// hidden behind 304 responses.
	entry.update(res.Header, false, f.now())
	return configFetchResult{config: config}, nil
}

func readLimitedConfig(reader io.Reader, maxSize int64) ([]byte, error) {
	limit := maxSize
	if maxSize < int64(^uint64(0)>>1) {
		limit++
	}
	body, err := io.ReadAll(io.LimitReader(reader, limit))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxSize {
		return nil, fmt.Errorf("config exceeds maximum size of %d bytes", maxSize)
	}
	return body, nil
}

func unbundleFetchedConfig(repo string, body []byte, key state.NyPublicKey) (*state.CentralCfg, error) {
	config, err := state.UnbundleConfig(string(body), key)
	if err != nil {
		return nil, fmt.Errorf("failed to unbundle config from %s: %w", repo, err)
	}
	return config, nil
}

func (entry *configCacheEntry) update(header http.Header, notModified bool, now time.Time) {
	if !notModified {
		entry.etag = header.Get("ETag")
		entry.lastModified = header.Get("Last-Modified")
		entry.cacheControl = header.Get("Cache-Control")
		entry.pragma = header.Get("Pragma")
		entry.expires = header.Get("Expires")
		entry.date = header.Get("Date")
		entry.age = header.Get("Age")
		entry.vary = header.Get("Vary")
	} else {
		updateCacheField(header, "ETag", &entry.etag)
		updateCacheField(header, "Last-Modified", &entry.lastModified)
		updateCacheField(header, "Cache-Control", &entry.cacheControl)
		updateCacheField(header, "Pragma", &entry.pragma)
		updateCacheField(header, "Expires", &entry.expires)
		updateCacheField(header, "Date", &entry.date)
		updateCacheField(header, "Age", &entry.age)
		updateCacheField(header, "Vary", &entry.vary)
	}

	cacheable, freshUntil := configFreshness(entry, now)
	if !cacheable {
		entry.valid = false
		entry.etag = ""
		entry.lastModified = ""
		entry.cacheControl = ""
		entry.pragma = ""
		entry.expires = ""
		entry.date = ""
		entry.age = ""
		entry.vary = ""
		entry.freshUntil = time.Time{}
		return
	}
	entry.valid = true
	entry.freshUntil = freshUntil
}

func updateCacheField(header http.Header, name string, destination *string) {
	if _, ok := header[http.CanonicalHeaderKey(name)]; ok {
		*destination = header.Get(name)
	}
}

func configFreshness(entry *configCacheEntry, now time.Time) (bool, time.Time) {
	directives := parseCacheControl(entry.cacheControl)
	if _, ok := directives["no-store"]; ok || strings.TrimSpace(entry.vary) == "*" {
		return false, time.Time{}
	}
	if _, ok := directives["no-cache"]; ok || strings.EqualFold(strings.TrimSpace(entry.pragma), "no-cache") {
		return true, time.Time{}
	}

	currentAge := responseCurrentAge(entry.date, entry.age, now)
	if rawMaxAge, ok := directives["max-age"]; ok {
		seconds, err := strconv.ParseInt(strings.Trim(rawMaxAge, `"`), 10, 64)
		if err == nil && seconds >= 0 {
			return true, freshnessDeadline(now, secondsDuration(seconds)-currentAge)
		}
	}

	expires, expiresErr := http.ParseTime(entry.expires)
	if expiresErr == nil {
		date, dateErr := http.ParseTime(entry.date)
		if dateErr != nil {
			date = now
		}
		return true, freshnessDeadline(now, expires.Sub(date)-currentAge)
	}
	return true, time.Time{}
}

func parseCacheControl(value string) map[string]string {
	directives := make(map[string]string)
	for part := range strings.SplitSeq(value, ",") {
		name, rawValue, found := strings.Cut(strings.TrimSpace(part), "=")
		name = strings.ToLower(name)
		if name == "" {
			continue
		}
		if found {
			directives[name] = strings.TrimSpace(rawValue)
		} else {
			directives[name] = ""
		}
	}
	return directives
}

func responseCurrentAge(dateValue, ageValue string, now time.Time) time.Duration {
	var apparentAge time.Duration
	if date, err := http.ParseTime(dateValue); err == nil && now.After(date) {
		apparentAge = now.Sub(date)
	}
	if seconds, err := strconv.ParseInt(strings.TrimSpace(ageValue), 10, 64); err == nil && seconds >= 0 {
		age := secondsDuration(seconds)
		if age > apparentAge {
			return age
		}
	}
	return apparentAge
}

func secondsDuration(seconds int64) time.Duration {
	maxSeconds := int64(^uint64(0)>>1) / int64(time.Second)
	if seconds > maxSeconds {
		seconds = maxSeconds
	}
	return time.Duration(seconds) * time.Second
}

func freshnessDeadline(now time.Time, remaining time.Duration) time.Time {
	if remaining <= 0 {
		return time.Time{}
	}
	return now.Add(remaining)
}

func (n *Nylon) updateConfigPollDelay(cfg *state.CentralCfg) {
	delay := n.CentralUpdateDelay
	if cfg != nil && cfg.Dist != nil && cfg.Dist.PollInterval != nil {
		delay = *cfg.Dist.PollInterval
	}
	if n.LocalCfg.Dist != nil && n.LocalCfg.Dist.PollInterval != nil {
		delay = *n.LocalCfg.Dist.PollInterval
	}
	n.configPollDelay.Store(int64(delay))
}

// responsible for central config distribution
func checkForConfigUpdates(n *Nylon) error {
	if n.CentralCfg.Dist == nil {
		return errors.New("nylon is not configured for automatic config distribution")
	}
	key := n.CentralCfg.Dist.Key
	currentTimestamp := n.Timestamp
	repos := append([]string(nil), n.CentralCfg.Dist.Repos...)
	if n.configFetcher == nil {
		n.configFetcher = newConfigFetcher(n.DNSResolver)
	}
	ctx := n.Context
	if ctx == nil {
		ctx = context.Background()
	}
	for _, repoStr := range repos {
		go func(repo string) {
			err := func() error {
				result, err := n.configFetcher.fetch(ctx, repo, key, n.MaxConfigSize)
				if err != nil {
					return err
				}
				if result.notModified {
					if n.DBG_log_repo_updates {
						n.Log.Debug("config repository has not changed", "repo", repo)
					}
					return nil
				}
				config := result.config
				if config.Timestamp <= currentTimestamp {
					if n.DBG_log_repo_updates {
						n.Log.Debug(fmt.Sprintf("found old update bundle at %s, skipping", repo))
					}
					return nil
				}
				n.Dispatch(func() error {
					if config.Timestamp <= n.Timestamp {
						return nil
					}
					n.Log.Info("Found a new config update in repo", "repo", repo)
					result, err := n.ApplyCentralConfig(config)
					if err != nil {
						if result != ApplyApplied {
							n.Log.Error("failed to apply central config update", "repo", repo, "result", result, "err", err)
							return nil
						}
						n.Log.Warn("central config applied with incomplete runtime reconciliation; will retry", "repo", repo, "err", err)
					}
					if n.ConfigPath != "" {
						bytes, err := yaml.Marshal(config)
						if err != nil {
							n.Log.Error("Error marshalling new config", "err", err.Error())
							return nil
						}
						err = os.WriteFile(n.ConfigPath, bytes, 0600)
						if err != nil {
							n.Log.Error("Error writing new config", "err", err.Error())
						}
					}
					return nil
				})
				return nil
			}()
			if err != nil {
				n.Log.Error("Error updating config", "err", err.Error())
			}
		}(repoStr)
	}
	return nil
}
