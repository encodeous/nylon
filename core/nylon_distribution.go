package core

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
)

// fetches and unbundles central config from url
func FetchConfig(repoStr string, key state.NyPublicKey, maxSize int64) (*state.CentralCfg, error) {
	return fetchConfig(repoStr, key, maxSize, state.NewDNSResolver(nil))
}

func fetchConfig(repoStr string, key state.NyPublicKey, maxSize int64, resolver *state.DNSResolver) (*state.CentralCfg, error) {
	repo, err := url.Parse(repoStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse repo URL %s: %w", repoStr, err)
	}
	cfgBody := make([]byte, 0)

	if repo.Scheme == "file" {
		file, err := os.ReadFile(repo.Opaque)
		if err != nil {
			return nil, fmt.Errorf("failed to read file %s: %w", repo.Opaque, err)
		}
		cfgBody = file
	} else if repo.Scheme == "http" || repo.Scheme == "https" {
		client := &http.Client{
			Transport: &http.Transport{
				DialContext: resolver.DialContext,
			},
		}
		res, err := client.Get(repo.String())
		if err != nil {
			return nil, fmt.Errorf("failed to fetch %s: %w", repo.String(), err)
		}
		cfgBody, err = io.ReadAll(io.LimitReader(res.Body, maxSize))
		if err != nil {
			res.Body.Close()
			return nil, fmt.Errorf("failed to read response from %s: %w", repo.String(), err)
		}
		err = res.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to close response from %s: %w", repo.String(), err)
		}
	}

	config, err := state.UnbundleConfig(string(cfgBody), key)
	if err != nil {
		return nil, fmt.Errorf("failed to unbundle config from %s: %w", repoStr, err)
	}
	return config, nil
}

// responsible for central config distribution
func checkForConfigUpdates(n *Nylon) error {
	if n.CentralCfg.Dist == nil {
		return errors.New("nylon is not configured for automatic config distribution")
	}
	key := n.CentralCfg.Dist.Key
	currentTimestamp := n.Timestamp
	repos := slices.Clone(n.CentralCfg.Dist.Repos)
	for _, repoStr := range repos {
		go func(repo string) {
			err := func() error {
				config, err := fetchConfig(repo, key, n.MaxConfigSize, n.DNSResolver)
				if err != nil {
					return err
				}
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
