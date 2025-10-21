package core

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/encodeous/nylon/state"
	"gopkg.in/yaml.v3"
)

// fetches and unbundles central config from url
func FetchConfig(repoStr string, key state.NyPublicKey) (*state.CentralCfg, error) {
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
		res, err := http.Get(repo.String())
		if err != nil {
			return nil, fmt.Errorf("failed to fetch %s: %w", repo.String(), err)
		}
		cfgBody, err = io.ReadAll(res.Body)
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
func checkForConfigUpdates(s *state.State) error {
	if s.CentralCfg.Dist == nil {
		return errors.New("nylon is not configured for automatic config distribution")
	}
	for _, repoStr := range s.CentralCfg.Dist.Repos {
		e := s.Env
		go func(repo string) {
			err := func() error {
				config, err := FetchConfig(repo, e.CentralCfg.Dist.Key)
				if err != nil {
					return err
				}
				if config.Timestamp > e.Timestamp && !s.Updating.Swap(true) {
					e.Log.Info("Found a new config update in repo", "repo", repo)
					bytes, err := yaml.Marshal(config)
					if err != nil {
						e.Log.Error("Error marshalling new config", "err", err.Error())
						goto err
					}
					err = os.WriteFile(e.ConfigPath, bytes, 0700)
					if err != nil {
						e.Log.Error("Error writing new config", "err", err.Error())
						goto err
					}
					e.Cancel(errors.New("shutting down for config update"))
					return nil
				err:
					s.Updating.Store(false)
				} else if state.DBG_log_repo_updates {
					e.Log.Debug(fmt.Sprintf("found old update bundle at %s, skipping", repo))
				}
				return nil
			}()
			if err != nil && state.DBG_log_repo_updates {
				e.Log.Error("Error updating config", "err", err.Error())
			}
		}(repoStr)
	}
	return nil
}
