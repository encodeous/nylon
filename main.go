//go:generate protoc -I . --go_out=. ./protocol/nylon.proto
package main

import (
	"github.com/encodeous/nylon/cmd"
)

func main() {
	cmd.Execute()
}
