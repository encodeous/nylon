package dp_wireguard

import (
	"encoding/base64"
	"fmt"
	"github.com/encodeous/nylon/state"
	"os"
	"path"
	"runtime"
)

func writeTmpConfig(s *state.State) (string, error) {
	tmp := path.Join(os.TempDir(), "nylon_wireguard", string(s.Id)+".conf")
	err := os.MkdirAll(path.Join(os.TempDir(), "nylon_wireguard"), 0700)
	if err != nil {
		return "", err
	}
	cfg := fmt.Sprintf(`
[Interface]
PrivateKey = %s
Address = %s
`,
		base64.StdEncoding.EncodeToString(s.WgKey.Bytes()),
		s.Key.Pubkey().DeriveNylonAddr().String())
	err = os.WriteFile(tmp, []byte(cfg), 0700)
	if err != nil {
		return "", err
	}
	return tmp, nil
}

func InitWireGuard(s *state.State) error {
	tmp, err := writeTmpConfig(s)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		err = InitWindows(tmp)
	} else {
		err = InitWgQuick(tmp)
	}
	if err != nil {
		s.Log.Error("Failed to initialize WireGuard tunnel, has Nylon gracefully shutdown?")
	}
	return nil
}

func CleanupWireGuard(s *state.State) error {
	tmp, err := writeTmpConfig(s)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		err = CleanupWindows(tmp)
	} else {
		err = CleanupWgQuick(tmp)
	}
	if err != nil {
		return err
	}
	return nil
}
