package cmd

import (
	"fmt"
	"github.com/encodeous/nylon/state"
	"github.com/manifoldco/promptui"
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
