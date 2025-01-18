package state

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
)

type EdPrivateKey ed25519.PrivateKey
type EdPublicKey ed25519.PublicKey
type EcPrivateKey ecdh.PrivateKey
type EcPublicKey ecdh.PublicKey
type Cert []byte

func MarshalPrivateKey[T any](key T) ([]byte, error) {
	data, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	data = pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: data,
	})
	return data, err
}
func UnmarshalPrivateKey[T any](data []byte) (T, error) {
	block, _ := pem.Decode(data)
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	return key.(T), err
}

func MarshalPublicKey[T any](key T) ([]byte, error) {
	data, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, err
	}
	data = pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: data,
	})
	return data, err
}
func UnmarshalPublicKey[T any](data []byte) (T, error) {
	block, _ := pem.Decode(data)
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	return key.(T), err
}

func (k EdPrivateKey) MarshalText() ([]byte, error) {
	return MarshalPrivateKey((ed25519.PrivateKey)(k))
}
func (k *EcPrivateKey) MarshalText() ([]byte, error) {
	return MarshalPrivateKey((*ecdh.PrivateKey)(k))
}

func (k EdPublicKey) MarshalText() ([]byte, error) {
	return MarshalPublicKey((ed25519.PublicKey)(k))
}
func (k *EcPublicKey) MarshalText() ([]byte, error) {
	return MarshalPublicKey((*ecdh.PublicKey)(k))
}

func (k *EdPrivateKey) UnmarshalText(text []byte) error {
	key, err := UnmarshalPrivateKey[ed25519.PrivateKey](text)
	*k = EdPrivateKey(key)
	return err
}
func (k *EcPrivateKey) UnmarshalText(text []byte) error {
	key, err := UnmarshalPrivateKey[*ecdh.PrivateKey](text)
	*k = EcPrivateKey(*key)
	return err
}

func (k *EdPublicKey) UnmarshalText(text []byte) error {
	key, err := UnmarshalPublicKey[ed25519.PublicKey](text)
	*k = EdPublicKey(key)
	return err
}
func (k *EcPublicKey) UnmarshalText(text []byte) error {
	key, err := UnmarshalPublicKey[*ecdh.PublicKey](text)
	*k = EcPublicKey(*key)
	return err
}

func (k *Cert) UnmarshalText(text []byte) error {
	block, _ := pem.Decode(text)
	*k = block.Bytes
	return nil
}

func (k Cert) MarshalText() ([]byte, error) {
	data := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: k,
	})
	return data, nil
}
