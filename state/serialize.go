package state

import (
	"encoding/base64"
	"fmt"
)

func (k NyPrivateKey) MarshalText() ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(k[:])), nil
}
func (k NyPublicKey) MarshalText() ([]byte, error) {
	return []byte(base64.StdEncoding.EncodeToString(k[:])), nil
}
func (k *NyPrivateKey) UnmarshalText(text []byte) error {
	data, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("failed to decode private key: %w", err)
	}
	if len(data) != KeySize {
		return fmt.Errorf("private key must decode to %d bytes, got %d", KeySize, len(data))
	}
	*k = NyPrivateKey(data)
	return nil
}
func (k *NyPublicKey) UnmarshalText(text []byte) error {
	data, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("failed to decode public key (%s): %w", text, err)
	}
	if len(data) != KeySize {
		return fmt.Errorf("public key must decode to %d bytes, got %d", KeySize, len(data))
	}
	*k = NyPublicKey(data)
	return nil
}
