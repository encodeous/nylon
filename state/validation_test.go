package state

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNameValidator_Valid(t *testing.T) {
	assert.NoError(t, NameValidator("1"))
	assert.NoError(t, NameValidator("ab_cd"))
	assert.NoError(t, NameValidator("abcd-a.com"))
}

func TestNameValidator_Invalid(t *testing.T) {
	assert.Error(t, NameValidator("1A"))
	assert.Error(t, NameValidator("node name"))
	assert.Error(t, NameValidator(""))
	assert.Error(t, NameValidator("\t"))
	assert.Error(t, NameValidator("abcd-a.com\\hi"))
	assert.Error(t, NameValidator(strings.Repeat("a", 200)))
}

func TestNodeConfigValidator_DnsResolver(t *testing.T) {
	assert.NoError(t, NodeConfigValidator(&LocalCfg{
		Id:           "valid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"1.1.1.1:53"},
	}))
	assert.NoError(t, NodeConfigValidator(&LocalCfg{
		Id:   "valid-node",
		Port: 5,
		Key:  [32]byte{1},
	}))
	assert.Error(t, NodeConfigValidator(&LocalCfg{
		Id:           "invalid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"google.com"},
	}))
	assert.Error(t, NodeConfigValidator(&LocalCfg{
		Id:           "invalid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"google.com:53"},
	}))
	assert.Error(t, NodeConfigValidator(&LocalCfg{
		Id:           "invalid-node",
		Port:         5,
		Key:          [32]byte{1},
		DnsResolvers: []string{"1.1.1.1"},
	}))
}

func TestCentralConfigValidator_OverlappingService(t *testing.T) {
	cfg := &CentralCfg{
		Services: map[ServiceId]netip.Prefix{
			"svc1": netip.MustParsePrefix("10.5.0.1/32"),
			"svc2": netip.MustParsePrefix("10.5.0.0/24"),
			"svc3": netip.MustParsePrefix("10.5.0.1/8"),
		},
	}
	assert.NoError(t, CentralConfigValidator(cfg))
}

func TestCentralConfigValidator_DuplicateService(t *testing.T) {
	cfg := &CentralCfg{
		Services: map[ServiceId]netip.Prefix{
			"svc1": netip.MustParsePrefix("10.5.0.1/32"),
			"svc2": netip.MustParsePrefix("10.5.0.1/24"),
			"svc3": netip.MustParsePrefix("10.5.0.1/32"),
		},
	}
	assert.Error(t, CentralConfigValidator(cfg))
}
