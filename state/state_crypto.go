package state

import (
	"github.com/encodeous/polyamide/device"
	"go.step.sm/crypto/x25519"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type NyPrivateKey [device.NoisePrivateKeySize]byte
type NyPublicKey [device.NoisePublicKeySize]byte

func GenerateKey() NyPrivateKey {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		panic(err)
	}
	return NyPrivateKey(key)
}

func (k NyPrivateKey) XPubkey() NyPublicKey {
	val, err := x25519.PrivateKey(k[:]).PublicKey()
	if err != nil {
		panic(err)
	}
	return NyPublicKey(val)
}

func (k NyPrivateKey) EdPubkey() []byte {
	val, err := x25519.PrivateKey(k[:]).PublicKey()
	if err != nil {
		panic(err)
	}
	val2, err := val.ToEd25519()
	if err != nil {
		panic(err)
	}
	return val2
}

func (k NyPublicKey) EdPubkey() []byte {
	val, err := (x25519.PublicKey(k[:])).ToEd25519()
	if err != nil {
		panic(err)
	}
	return val
}
