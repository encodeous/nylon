package impl

import (
	"errors"
	"github.com/encodeous/nylon/state"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"net/url"
	"os"
)

// responsible for central config distribution
func checkForConfigUpdates(s *state.State) error {
	if s.Dist == nil {
		return errors.New("nylon is not configured for automatic config distribution")
	}
	for _, repoStr := range s.Dist.Repos {
		repo, err := url.Parse(repoStr)
		if err != nil {
			return err
		}
		e := s.Env
		go func() {
			err := func() error {
				cfgBody := make([]byte, 0)

				if repo.Scheme == "file" {
					file, err := os.ReadFile(repo.Opaque)
					if err != nil {
						return err
					}
					cfgBody = file
				} else if repo.Scheme == "http" || repo.Scheme == "https" {
					res, err := http.Get(repo.String())
					if err != nil {
						return err
					}
					cfgBody, err = io.ReadAll(res.Body)
					if err != nil {
						return err
					}
					err = res.Body.Close()
					if err != nil {
						return err
					}
				}

				config, err := state.UnbundleConfig(string(cfgBody), e.Dist.Key)
				if err != nil {
					return err
				}
				if config.Timestamp > e.Timestamp && !s.Updating.Swap(true) {
					e.Log.Info("Found a new config update in repo", "repo", repo.String())
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
					e.Log.Debug("found old update bundle, skipping")
				}
				return nil
			}()
			if err != nil && state.DBG_log_repo_updates {
				e.Log.Error("Error updating config", "err", err.Error())
			}
		}()
	}
	return nil
}
