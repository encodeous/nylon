package state

import (
	"crypto"
	"crypto/rand"
	"encoding/base64"
	"net/netip"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"go.step.sm/crypto/x25519"
	"golang.org/x/crypto/chacha20poly1305"
)

func TestBundleUnbundle(t *testing.T) {
	root := GenerateKey()
	cfg := CentralCfg{
		Dist:    nil,
		Routers: make([]RouterCfg, 0),
		Clients: []ClientCfg{
			{NodeCfg{
				Id:     "blah",
				PubKey: NyPublicKey{},
				Prefixes: []PrefixHealthWrapper{
					{
						&StaticPrefixHealth{
							Prefix: netip.MustParsePrefix("10.0.0.1/32"),
							Metric: 0,
						},
					},
				},
			}},
		},
		Graph: []string{
			"blah, blah",
			"a = blah",
			"b = a",
			"a, b",
		},
		Timestamp: 0,
	}
	txt, err := yaml.Marshal(cfg)
	assert.NoError(t, err)
	bundle, err := BundleConfig(string(txt), root)
	assert.NoError(t, err)

	finalCfg, err := UnbundleConfig(bundle, root.Pubkey())
	assert.NoError(t, err)
	finalCfg.Timestamp = 0 // cannot enforce timestamp to be the same
	assert.EqualValues(t, cfg, *finalCfg)
}

func TestBundleTamper(t *testing.T) {
	root := GenerateKey()
	cfg := CentralCfg{
		Dist:    nil,
		Routers: make([]RouterCfg, 0),
		Clients: []ClientCfg{
			{NodeCfg{
				Id:     "blah",
				PubKey: NyPublicKey{},
				Prefixes: []PrefixHealthWrapper{
					{
						&StaticPrefixHealth{
							Prefix: netip.MustParsePrefix("10.0.0.1/32"),
							Metric: 0,
						},
					},
					{
						&StaticPrefixHealth{
							Prefix: netip.MustParsePrefix("10.0.0.2/32"),
							Metric: 0,
						},
					},
					{
						&StaticPrefixHealth{
							Prefix: netip.MustParsePrefix("10.0.0.3/8"),
							Metric: 0,
						},
					},
				},
			}},
		},
		Graph: []string{
			"blah, blah",
			"a = blah",
			"b = a",
			"a, b",
		},
		Timestamp: 0,
	}
	txt, err := yaml.Marshal(cfg)
	assert.NoError(t, err)
	bundle, err := BundleConfig(string(txt), root)
	assert.NoError(t, err)

	buf := []byte(bundle)
	if buf[0] == 'a' {
		buf[0] = 'b'
	} else {
		buf[0] = 'a'
	}
	bundle = string(buf)

	_, err = UnbundleConfig(bundle, root.Pubkey())
	assert.ErrorContains(t, err, "message authentication failed")
}

func TestBundleInvalidSign(t *testing.T) {
	root := GenerateKey()
	fakePriv := GenerateKey()
	cfg := CentralCfg{
		Dist:    nil,
		Routers: make([]RouterCfg, 0),
		Clients: []ClientCfg{
			{NodeCfg{
				Id:     "blah",
				PubKey: NyPublicKey{},
			}},
		},
		Graph: []string{
			"blah, blah",
			"a = blah",
			"b = a",
			"a, b",
		},
		Timestamp: 0,
	}
	cfg.Timestamp = time.Now().UnixNano()

	plainText, err := yaml.Marshal(cfg)
	assert.NoError(t, err)
	privKey := x25519.PrivateKey(fakePriv[:])
	sig, err := privKey.Sign(rand.Reader, plainText, crypto.Hash(0))
	assert.NoError(t, err)

	plainBundle := append(sig, plainText...)

	pk := root.Pubkey()
	ahead, err := chacha20poly1305.NewX(pk[:])
	assert.NoError(t, err)
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	_, err = rand.Read(nonce)
	assert.NoError(t, err)
	cipherText := ahead.Seal(make([]byte, 0), nonce, plainBundle, make([]byte, 0))
	bundle := base64.StdEncoding.EncodeToString(append(nonce, cipherText...))

	_, err = UnbundleConfig(bundle, root.Pubkey())
	assert.ErrorContains(t, err, "invalid signature")
}

func TestBundleInvalidData1(t *testing.T) {
	root := GenerateKey()
	bundle := base64.StdEncoding.EncodeToString([]byte("blah"))
	_, err := UnbundleConfig(bundle, root.Pubkey())
	assert.ErrorContains(t, err, "invalid bundle, too small")
}

func TestBundleInvalidData2(t *testing.T) {
	root := GenerateKey()
	bundle, err := SignBundle([]byte("blah"), root)
	assert.NoError(t, err)
	pub := root.Pubkey()
	bundle, err = SealBundle(bundle, pub[:])
	assert.NoError(t, err)

	_, err = UnbundleConfig(base64.StdEncoding.EncodeToString(bundle), root.Pubkey())
	assert.Error(t, err)
}
