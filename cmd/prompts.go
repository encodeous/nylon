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

func promptCreateNode() state.LocalCfg {
	nodeCfg := state.LocalCfg{
		Key: state.GenerateKey(),
		Id:  "my-node",
	}

	fmt.Println("Nylon Initialization Wizard")

	fmt.Println("Node Configuration")
	fmt.Println("Give this node a name:")
	nodeCfg.Id = state.NodeId(promptDefaultStr("name", string(nodeCfg.Id), state.NameValidator))

	fmt.Println("What port should nylon listen on?:")
	nodeCfg.Port = promptDefaultPort("[UDP] port", "57175", state.PortValidator)

	pkStr, err := nodeCfg.Key.XPubkey().MarshalText()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Your node public key is: %s. Add this to the central config on every node\n", string(pkStr))

	fmt.Println("Where should the node config be saved?:")
	nodeConfigPath = safeSaveFile(nodeConfigPath, "Node Config")
	return nodeCfg
}

func promptSelectRouter(central state.CentralCfg) state.NodeId {
	routers := make([]state.NodeId, 0)
	for _, router := range central.Routers {
		routers = append(routers, router.Id)
	}
	if len(routers) == 0 {
		panic("no routers configured")
	}
	prompt := promptui.Select{
		Label:             "router",
		Items:             routers,
		StartInSearchMode: true,
	}
	_, node, err := prompt.Run()
	if err != nil {
		panic(err)
	}
	return state.NodeId(node)
}
