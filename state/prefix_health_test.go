package state

import (
	"net/netip"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
)

func TestPrefixHealthSerialization(t *testing.T) {
	tests := []struct {
		name    string
		wrapper PrefixHealthWrapper
		yamlStr string
	}{
		{
			name: "StaticPrefixHealth",
			wrapper: PrefixHealthWrapper{
				PrefixHealth: &StaticPrefixHealth{
					Prefix: netip.MustParsePrefix("10.0.0.0/24"),
					Metric: 100,
				},
			},
			yamlStr: `type: static
prefix: 10.0.0.0/24
metric: 100
`,
		},
		{
			name: "PingPrefixHealth",
			wrapper: PrefixHealthWrapper{
				PrefixHealth: &PingPrefixHealth{
					Prefix:      netip.MustParsePrefix("192.168.1.0/24"),
					Addr:        netip.MustParseAddr("8.8.8.8"),
					MaxFailures: 3,
					Delay:       10 * time.Second,
				},
			},
			yamlStr: `type: ping
prefix: 192.168.1.0/24
addr: 8.8.8.8
max_failures: 3
delay: 10s
`,
		},
		{
			name: "HTTPPrefixHealth",
			wrapper: PrefixHealthWrapper{
				PrefixHealth: &HTTPPrefixHealth{
					Prefix: netip.MustParsePrefix("172.16.0.0/16"),
					URL:    "http://example.com/health",
					Delay:  5 * time.Second,
				},
			},
			yamlStr: `type: http
prefix: 172.16.0.0/16
url: http://example.com/health
delay: 5s
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" Marshal", func(t *testing.T) {
			data, err := yaml.Marshal(&tt.wrapper)
			assert.NoError(t, err)
			assert.YAMLEq(t, tt.yamlStr, string(data))
		})

		t.Run(tt.name+" Unmarshal", func(t *testing.T) {
			var wrapper PrefixHealthWrapper
			err := yaml.Unmarshal([]byte(tt.yamlStr), &wrapper)
			assert.NoError(t, err)
			assert.NotNil(t, wrapper.PrefixHealth)
			assert.Equal(t, tt.wrapper.GetPrefix(), wrapper.GetPrefix())
		})

		t.Run(tt.name+" RoundTrip", func(t *testing.T) {
			// Marshal
			data, err := yaml.Marshal(&tt.wrapper)
			assert.NoError(t, err)

			// Unmarshal
			var wrapper PrefixHealthWrapper
			err = yaml.Unmarshal(data, &wrapper)
			assert.NoError(t, err)

			// Verify
			assert.Equal(t, tt.wrapper.GetPrefix(), wrapper.GetPrefix())

			switch orig := tt.wrapper.PrefixHealth.(type) {
			case *StaticPrefixHealth:
				result, ok := wrapper.PrefixHealth.(*StaticPrefixHealth)
				assert.True(t, ok)
				assert.Equal(t, orig.Metric, result.Metric)
			case *PingPrefixHealth:
				result, ok := wrapper.PrefixHealth.(*PingPrefixHealth)
				assert.True(t, ok)
				assert.Equal(t, orig.Addr, result.Addr)
				assert.Equal(t, orig.MaxFailures, result.MaxFailures)
				assert.Equal(t, orig.Delay, result.Delay)
			case *HTTPPrefixHealth:
				result, ok := wrapper.PrefixHealth.(*HTTPPrefixHealth)
				assert.True(t, ok)
				assert.Equal(t, orig.URL, result.URL)
				assert.Equal(t, orig.Delay, result.Delay)
			}
		})
	}
}
