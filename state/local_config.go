package state

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// ParseLocalConfig rejects distribution settings that belong in central.yaml.
func ParseLocalConfig(data []byte) (*LocalCfg, error) {
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if _, exists := fields["dist"]; exists {
		return nil, fmt.Errorf("dist is no longer supported in node.yaml; configure distribution in central.yaml")
	}
	var cfg LocalCfg
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
