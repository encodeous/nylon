package state

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestPubkey(t *testing.T) {
	priv := NyPrivateKey{}
	// from wg genkey
	err := priv.UnmarshalText([]byte("sE7wuHwS06cQRlCKnbGVva6UcGaKMDLtWD4GghORWFg="))
	assert.NoError(t, err)

	pub := priv.Pubkey()
	pubStr, err := pub.MarshalText()
	assert.NoError(t, err)
	// from wg pubkey
	assert.Equal(t, string(pubStr), "ynMTsT/6Is4mNsYAYp5nR98LEuUSz3AkwOCvMkT5fj8=")
}

func TestGenerateKey(t *testing.T) {
	key := GenerateKey()
	pub := key.Pubkey()
	_, err := pub.MarshalText()
	assert.NoError(t, err)
}
