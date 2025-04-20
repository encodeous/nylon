package state

import (
	"crypto/rand"
	"github.com/encodeous/nylon/polyamide/device"
	"go.step.sm/crypto/x25519"
)

type NyPrivateKey [device.NoisePrivateKeySize]byte
type NyPublicKey [device.NoisePublicKeySize]byte

func GenerateKey() NyPrivateKey {
	key := make([]byte, device.NoisePrivateKeySize)

	if _, err := rand.Read(key); err != nil {
		panic(err)
	}

	// Modify random bytes using algorithm described at:
	// https://cr.yp.to/ecdh.html.
	key[0] &= 248
	key[31] &= 127
	key[31] |= 64
	return NyPrivateKey(key)
}

func (k NyPrivateKey) Pubkey() NyPublicKey {
	val, err := x25519.PrivateKey(k[:]).PublicKey()
	if err != nil {
		panic(err)
	}
	return NyPublicKey(val)
}
