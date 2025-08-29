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
