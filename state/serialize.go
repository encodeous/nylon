package state

import (
	"encoding/base64"
	"fmt"
	"strings"
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
		return err
	}
	*k = NyPrivateKey(data)
	return nil
}
func (k *NyPublicKey) UnmarshalText(text []byte) error {
	data, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return err
	}
	*k = NyPublicKey(data)
	return nil
}

func (p Pair[N, M]) MarshalText() ([]byte, error) {
	return []byte(fmt.Sprintf("%v, %v", p.V1, p.V2)), nil
}
func (p *Pair[N, M]) UnmarshalText(text []byte) error {
	str := string(text)
	pairv := strings.Split(str, ",")
	if len(pairv) != 2 {
		return fmt.Errorf("invalid pair: %s", str)
	}
	v1 := strings.TrimSpace(pairv[0])
	if err := NameValidator(v1); err != nil {
		return err
	}
	v2 := strings.TrimSpace(pairv[1])
	if err := NameValidator(v2); err != nil {
		return err
	}
	switch t := any(p).(type) {
	case *Pair[NodeId, NodeId]:
		t.V1 = NodeId(v1)
		t.V2 = NodeId(v2)
	default:
		return fmt.Errorf("unknown pair type: %T", t)
	}
	return nil
}
