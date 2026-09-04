package state

import (
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDistributionPollIntervalYAML(t *testing.T) {
	var cfg CentralCfg
	err := yaml.Unmarshal([]byte(`
dist:
  repos: [https://example.com/config.nybundle]
  poll_interval: 45s
`), &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Dist)
	require.NotNil(t, cfg.Dist.PollInterval)
	assert.Equal(t, 45*time.Second, *cfg.Dist.PollInterval)
}

func TestDistributionPollIntervalMustBePositive(t *testing.T) {
	interval := time.Duration(0)
	cfg := CentralCfg{Dist: &DistributionCfg{PollInterval: &interval}}
	err := CentralConfigValidator(&cfg)
	assert.ErrorContains(t, err, "poll_interval must be greater than 0")
}

func TestLocalDistributionPollIntervalYAML(t *testing.T) {
	var cfg LocalCfg
	err := yaml.Unmarshal([]byte(`
dist:
  url: https://example.com/config.nybundle
  poll_interval: 2m
`), &cfg)
	require.NoError(t, err)
	require.NotNil(t, cfg.Dist)
	require.NotNil(t, cfg.Dist.PollInterval)
	assert.Equal(t, 2*time.Minute, *cfg.Dist.PollInterval)
}

func TestLocalDistributionPollIntervalMustBePositive(t *testing.T) {
	interval := time.Duration(0)
	cfg := LocalCfg{
		Key:  GenerateKey(),
		Id:   "node",
		Port: 1,
		Dist: &LocalDistributionCfg{
			Url:          "https://example.com/config.nybundle",
			PollInterval: &interval,
		},
	}
	err := NodeConfigValidator(nil, &cfg)
	assert.ErrorContains(t, err, "poll_interval must be greater than 0")
}
