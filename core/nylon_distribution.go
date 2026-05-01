package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"

	"github.com/encodeous/nylon/state"
	"github.com/goccy/go-yaml"
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
		client := &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network string, addr string) (conn net.Conn, err error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					addrs, err := state.ResolveName(ctx, host)
					if err != nil {
						return nil, err
					}
					for _, ip := range addrs {
						var dialer net.Dialer
						conn, err = dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
						if err == nil {
							break
						}
					}
					return
				},
			},
		}
		res, err := client.Get(repo.String())
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
				config, err := FetchConfig(repo, key)
				if err != nil {
					return err
				}
				if config.Timestamp <= currentTimestamp {
					if state.DBG_log_repo_updates {
						n.Log.Debug(fmt.Sprintf("found old update bundle at %s, skipping", repo))
					}
					return nil
				}
				n.Dispatch(func() error {
					if config.Timestamp <= n.Timestamp {
						return nil
					}
					n.Log.Info("Found a new config update in repo", "repo", repo)
					result, err := n.ApplyCentralConfig(*config)
					if err != nil {
						n.Log.Error("failed to apply central config update", "repo", repo, "result", result, "err", err)
						return nil
					}
					if n.ConfigPath != "" {
						bytes, err := yaml.Marshal(config)
						if err != nil {
							n.Log.Error("Error marshalling new config", "err", err.Error())
							return nil
						}
						err = os.WriteFile(n.ConfigPath, bytes, 0700)
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
