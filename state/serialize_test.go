package state

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
)

func TestSerialize(t *testing.T) {
	cfg, ks := SampleNetwork(t, 50, 50, true)
	env := SampleEnv(&cfg, ks, "router-1")

	// test node local config
	x1, err := yaml.Marshal(env.LocalCfg)
	assert.NoError(t, err)
	y1 := LocalCfg{}
	err = yaml.Unmarshal(x1, &y1)
	assert.NoError(t, err)
	assert.EqualValues(t, env.LocalCfg, y1)

	// test central config
	x2, err := yaml.Marshal(env.CentralCfg)
	assert.NoError(t, err)
	y2 := CentralCfg{}
	err = yaml.Unmarshal(x2, &y2)
	assert.NoError(t, err)
	assert.EqualValues(t, env.CentralCfg, y2)
}

func TestDeserializeInvalid(t *testing.T) {
	// test node local config
	x1 := `key: 6NJn1youOZPElIzmzzios2JA3bZjiGWg8blU/IGowHc=
id: router-1
port: abcd
`
	y1 := LocalCfg{}
	err := yaml.Unmarshal([]byte(x1), &y1)
	assert.ErrorContains(t, err, "cannot unmarshal string")
}
