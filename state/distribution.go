package state

import (
	"crypto"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/goccy/go-yaml"
	"go.step.sm/crypto/x25519"
	"golang.org/x/crypto/chacha20poly1305"
)

func SignBundle(data []byte, key NyPrivateKey) ([]byte, error) {
	privKey := x25519.PrivateKey(key[:])
	sig, err := privKey.Sign(rand.Reader, data, crypto.Hash(0))
	if err != nil {
		return nil, err
	}
	return append(sig, data...), nil
}

func VerifyBundle(data []byte, key NyPublicKey) ([]byte, error) {
	if len(data) < x25519.SignatureSize {
		return nil, errors.New("invalid signature: too short")
	}
	signature := data[:x25519.SignatureSize]
	plainText := data[x25519.SignatureSize:]
	if !x25519.Verify(key[:], plainText, signature) {
		return nil, errors.New("invalid signature")
	}
	return plainText, nil
}

func SealBundle(data []byte, key []byte) ([]byte, error) {
	ahead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, chacha20poly1305.NonceSizeX)
	_, err = rand.Read(nonce)
	if err != nil {
		return nil, err
	}
	cipherText := ahead.Seal(make([]byte, 0), nonce, data, make([]byte, 0))
	return append(nonce, cipherText...), nil
}

func OpenBundle(data []byte, key []byte) ([]byte, error) {
	if len(data) < chacha20poly1305.NonceSizeX {
		return nil, errors.New("invalid bundle, too small")
	}
	ahead, err := chacha20poly1305.NewX(key[:])
	if err != nil {
		return nil, err
	}
	nonce := data[:chacha20poly1305.NonceSizeX]
	cipherText := data[chacha20poly1305.NonceSizeX:]
	bundle, err := ahead.Open(make([]byte, 0), nonce, cipherText, make([]byte, 0))
	if err != nil {
		return nil, err
	}
	return bundle, nil
}

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

	bundle, err := SignBundle(plainText, rootKey)
	if err != nil {
		return "", err
	}
	pub := rootKey.Pubkey()
	bundle, err = SealBundle(bundle, pub[:])
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(bundle), nil
}

func UnbundleConfig(bundleStr string, pubKey NyPublicKey) (*CentralCfg, error) {
	bundle, err := base64.StdEncoding.DecodeString(bundleStr)
	if err != nil {
		return nil, err
	}
	bundle, err = OpenBundle(bundle, pubKey[:])
	if err != nil {
		return nil, err
	}
	bundle, err = VerifyBundle(bundle, pubKey)
	if err != nil {
		return nil, err
	}

	cfg := &CentralCfg{}
	err = yaml.Unmarshal(bundle, cfg)
	if err != nil {
		return nil, err
	}
	err = CentralConfigValidator(cfg)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}
