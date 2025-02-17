package nylon_dp

import (
	"encoding/base64"
	"fmt"
	"github.com/encodeous/nylon/state"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"os"
	"path"
)

func getTmpConfigPath(s *state.State) string {
	return path.Join(os.TempDir(), "nylon_wireguard", string(s.Id)+".conf")
}

func makeClientConfig(s *state.State) string {
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
		return ""
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
	return cfg
}
