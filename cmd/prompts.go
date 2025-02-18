package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/manifoldco/promptui"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
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

func promptDefaultPort(label string, def string, validateFunc promptui.ValidateFunc) uint16 {
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
	port16, err := strconv.ParseUint(val, 10, 16)
	return uint16(port16)
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
	nodeCfg := state.NodeCfg{
		Key: state.GenerateKey(),
		Id:  "my-node",
	}

	fmt.Println("Nylon Initialization Wizard")

	fmt.Println("Node Configuration")
	fmt.Println("Give this node a name:")
	nodeCfg.Id = state.Node(promptDefaultStr("name", string(nodeCfg.Id), state.NameValidator))

	fmt.Println("Where should the control-plane listen to?:")
	nodeCfg.CtlBind = promptDefaultAddrPort("[TCP] ip:port", "0.0.0.0:54003", state.BindValidator)
	fmt.Println("What port should the data-plane listen to?:")
	nodeCfg.DpPort = promptDefaultPort("[UDP] port", "54004", state.PortValidator)

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
