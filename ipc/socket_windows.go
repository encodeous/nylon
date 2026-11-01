package ipc

import (
	"net"
	"time"

	"github.com/encodeous/polyamide-wireguard-go/ipc/namedpipe"
	"golang.org/x/sys/windows"
)

// Only SYSTEM and Administrators can open it, at high integrity.
var securityDescriptor *windows.SECURITY_DESCRIPTOR

func init() {
	var err error
	securityDescriptor, err = windows.SecurityDescriptorFromString("O:SYD:P(A;;GA;;;SY)(A;;GA;;;BA)S:(ML;;NWNRNX;;;HI)")
	if err != nil {
		panic(err)
	}
}

// Path is where the daemon for interface itf listens. Only administrators can create pipes under ProtectedPrefix,
// so another program can't pose as nylon.
func Path(itf string) string {
	return `\\.\pipe\ProtectedPrefix\Administrators\nylon\` + itf
}

// Listen opens the pipe for interface itf.
func Listen(itf string) (net.Listener, error) {
	return (&namedpipe.ListenConfig{SecurityDescriptor: securityDescriptor}).Listen(Path(itf))
}

func dial(itf string) (net.Conn, error) {
	return namedpipe.DialTimeout(Path(itf), 5*time.Second)
}
