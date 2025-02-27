package state

import (
	"crypto"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"go.step.sm/crypto/x25519"
	"golang.org/x/crypto/chacha20poly1305"
	"gopkg.in/yaml.v3"
	"time"
)

// BundleConfig first signs the config with the root private key, ensuring the authenticity, then encrypts the message using the bytes of the root public key as the shared key, offering some level of privacy. (assuming the root public key is not shared widely)
func BundleConfig(config string, rootKey NyPrivateKey) (string, error) {
	cfg := CentralCfg{}
	err := yaml.Unmarshal([]byte(config), &cfg)
	if err != nil {
		return "", err
	}
	err = CentralConfigValidator(&cfg)
	if err != nil {
		return "", err
	}
	cfg.Timestamp = time.Now().UnixNano()

	plainText, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	privKey := x25519.PrivateKey(rootKey[:])
	sig, err := privKey.Sign(rand.Reader, plainText, crypto.Hash(0))
	if err != nil {
		return "", err
	}

	bundle := append(sig, plainText...)

	pk := rootKey.XPubkey()
	ahead, err := chacha20poly1305.NewX(pk[:])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	_, err = rand.Read(nonce)
	if err != nil {
		return "", err
	}
	cipherText := ahead.Seal(make([]byte, 0), nonce, bundle, make([]byte, 0))
	return base64.StdEncoding.EncodeToString(append(nonce, cipherText...)), nil
}

func UnbundleConfig(bundleStr string, pubKey NyPublicKey) (*CentralCfg, error) {
	finalBundle, err := base64.StdEncoding.DecodeString(bundleStr)
	if err != nil {
		return nil, err
	}
	if len(finalBundle) < chacha20poly1305.NonceSizeX+x25519.SignatureSize {
		return nil, errors.New("invalid config, too small")
	}
	ahead, err := chacha20poly1305.NewX(pubKey[:])
	if err != nil {
		return nil, err
	}
	nonce := finalBundle[:chacha20poly1305.NonceSizeX]
	cipherText := finalBundle[chacha20poly1305.NonceSizeX:]
	bundle, err := ahead.Open(make([]byte, 0), nonce, cipherText, make([]byte, 0))
	if err != nil {
		return nil, err
	}

	signature := bundle[:x25519.SignatureSize]
	plainText := bundle[x25519.SignatureSize:]
	if !x25519.Verify(pubKey[:], plainText, signature) {
		return nil, errors.New("invalid signature")
	}
	cfg := &CentralCfg{}
	err = yaml.Unmarshal(plainText, cfg)
	if err != nil {
		return nil, err
	}
	err = CentralConfigValidator(cfg)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
