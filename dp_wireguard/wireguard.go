package dp_wireguard

import (
	"encoding/base64"
	"fmt"
	"github.com/encodeous/nylon/state"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"os"
	"path"
	"runtime"
)

func getTmpConfigPath(s *state.State) string {
	return path.Join(os.TempDir(), "nylon_wireguard", string(s.Id)+".conf")
}

func writeTmpConfig(s *state.State) (string, error) {
	tmp := getTmpConfigPath(s)
	err := os.MkdirAll(path.Join(os.TempDir(), "nylon_wireguard"), 0700)
	if err != nil {
		return "", err
	}

	peerCfg := ""
	for _, peer := range s.Nodes {
		if peer.Id == s.Id {
			continue
		}
		peerCfg = peerCfg + fmt.Sprintf(`[Peer]
PublicKey = %s
AllowedIPs = %s/%d
`, wgtypes.Key(peer.DpPubKey.Bytes()).String(), peer.NylonAddr, peer.NylonAddr.BitLen())
	}

	selfPub, err := s.GetPubNodeCfg(s.Id)
	if err != nil {
		return "", err
	}

	cfg := fmt.Sprintf(`
[Interface]
PrivateKey = %s
Address = %s

%s
`,
		base64.StdEncoding.EncodeToString(s.WgKey.Bytes()),
		selfPub.NylonAddr,
		peerCfg)
	err = os.WriteFile(tmp, []byte(cfg), 0700)
	s.Log.Debug("written wg config file", "path", tmp)
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
		err = InitWgQuick(tmp, s.Env)
	}
	if err != nil {
		s.Log.Error("Failed to initialize WireGuard tunnel, has Nylon gracefully shutdown?")
	}
	return nil
}

func CleanupWireGuard(s *state.State) error {
	tmp := getTmpConfigPath(s)
	var err error
	if runtime.GOOS == "windows" {
		err = CleanupWindows(tmp)
	} else {
		err = CleanupWgQuick(tmp, s.Env)
	}
	if err != nil {
		return err
	}
	return nil
}
