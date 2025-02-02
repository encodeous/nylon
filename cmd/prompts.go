package cmd

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/manifoldco/promptui"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"net/netip"
	"os"
	"path/filepath"
)

func promptDefaultStr(label string, def string, validateFunc promptui.ValidateFunc) string {
	prompt := promptui.Prompt{
		Label:     label,
		Default:   def,
		AllowEdit: true,
		Validate:  validateFunc,
	}
	val, err := prompt.Run()
	if err != nil {
		panic(err)
	}
	return val
}

func promptYN(prefix string, def bool) bool {
	choose := promptui.Select{
		Label:     prefix,
		Items:     []string{"Yes", "No"},
		Size:      2,
		CursorPos: 0,
	}
	if !def {
		choose.CursorPos = 1
	}
	run, _, err := choose.Run()
	if err != nil {
		return false
	}
	if run == 0 {
		return true
	} else {
		return false
	}
}

func promptDefaultAddrPort(label string, def string, validateFunc promptui.ValidateFunc) netip.AddrPort {
	prompt := promptui.Prompt{
		Label:     label,
		Default:   def,
		AllowEdit: true,
		Validate:  validateFunc,
	}
	val, err := prompt.Run()
	if err != nil {
		panic(err)
	}
	addr, err := netip.ParseAddrPort(val)
	return addr
}

func promptDefaultAddr(label string, def string, validateFunc promptui.ValidateFunc) netip.Addr {
	prompt := promptui.Prompt{
		Label:     label,
		Default:   def,
		AllowEdit: true,
		Validate:  validateFunc,
	}
	val, err := prompt.Run()
	if err != nil {
		panic(err)
	}
	addr, err := netip.ParseAddr(val)
	return addr
}

func safeSaveFile(path string, name string) string {
Save:
	path, err := filepath.Abs(path)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Where do you want to save the %s?", name)
	path = promptDefaultStr("path", path, state.PathValidator)

	if _, err := os.Stat(path); os.IsExist(err) {
		fmt.Printf("Warning: %s file already exists: %s, do you want to overwrite it?\n", name, path)
		res := promptYN("Overwrite?", false)
		if !res {
			goto Save
		}
	}
	return path
}

func promptCreateNode() state.NodeCfg {
	dpKey, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		panic(err)
	}
	ecKey, err := ecdh.X25519().NewPrivateKey(dpKey[:])
	if err != nil {
		panic(err)
	}
	_, ctlKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	nodeCfg := state.NodeCfg{
		Key:   state.EdPrivateKey(ctlKey),
		WgKey: (*state.EcPrivateKey)(ecKey),
		Id:    "my-node",
	}

	fmt.Println("Nylon Initialization Wizard")

	fmt.Println("Node Configuration")
	fmt.Println("Give this node a name:")
	nodeCfg.Id = state.Node(promptDefaultStr("name", string(nodeCfg.Id), state.NameValidator))

	fmt.Println("Where should the control-plane listen to?:")
	nodeCfg.CtlBind = promptDefaultAddrPort("[TCP] ip:port", "0.0.0.0:54003", state.BindValidator)
	fmt.Println("Where should the data-plane (WireGuard) listen to?:")
	nodeCfg.DpBind = promptDefaultAddrPort("[UDP] ip:port", "0.0.0.0:54004", state.BindValidator)
	fmt.Println("Where should the data-plane probe (for discovery & metric) listen to?:")
	nodeCfg.ProbeBind = promptDefaultAddrPort("[UDP] ip:port", "0.0.0.0:54003", state.BindValidator)

	fmt.Println("\nNOTE: You should make these ports accessible for best reliability and performance.\nIf it is not possible, as long as one node in the network is reachable, nylon can still work!\n\n")

	nodeConfigPath = safeSaveFile(nodeConfigPath, "Node Config")
	return nodeCfg
}

func promptGenPubCfg(cfg state.NodeCfg) state.PubNodeCfg {
	fmt.Println("What is your publicly accessible ip address?:")
	ext := promptDefaultAddr("ip - optional", "127.0.0.1", state.AddrValidator)
	fmt.Println("What do you want your Nylon address to be?:")
	nylIp := promptDefaultAddr("ip - optional", "10.0.0.1", state.AddrValidator)
	return cfg.GeneratePubCfg(ext, nylIp)
}
