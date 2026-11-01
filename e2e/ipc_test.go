//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/encodeous/nylon/state"
)

// Speaks the wire format byte by byte, so a change to it shows up here even if client and server change together.
func TestIPCRawSocketBinaryProtocol(t *testing.T) {
	h := NewHarness(t)
	configDir := h.SetupTestDir()

	key := state.GenerateKey()
	central := state.CentralCfg{
		Routers: []state.RouterCfg{
			SimpleRouter("node1", key.Pubkey(), "10.0.0.1", ""),
		},
	}
	centralPath := h.WriteConfig(configDir, "central.yaml", central)
	nodePath := h.WriteConfig(configDir, "node1.yaml", SimpleLocal("node1", key))

	h.StartNodes(NodeSpec{
		Name:              "node1",
		IP:                "",
		CentralConfigPath: centralPath,
		NodeConfigPath:    nodePath,
	})

	// hello, then a frame holding IpcRequest{id: 1, status: {}}
	stdout, _, err := h.Exec("node1", []string{
		"python3",
		"-c",
		`import socket, struct
s=socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect("/var/run/nylon/nylon0.sock")
s.sendall(b"NYLN\x00\x01" + struct.pack(">I", 4) + b"\x08\x01\x12\x00")
def read(n):
    data=b""
    while len(data) < n:
        chunk=s.recv(n - len(data))
        if not chunk:
            raise EOFError
        data+=chunk
    return data
print(read(6).hex())
size=struct.unpack(">I", read(4))[0]
print(read(size)[:3].hex())
s.close()
`,
	})
	if err != nil {
		t.Fatalf("raw IPC request failed: %v\nstdout:\n%s", err, stdout)
	}
	lines := strings.Fields(stdout)
	if len(lines) != 2 {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if lines[0] != "4e594c4e0001" {
		t.Fatalf("hello was %s, want NYLN version 1", lines[0])
	}
	// id 1 (08 01), then field 3, the status (1a), and no field 2, an error
	if lines[1] != "08011a" {
		t.Fatalf("response started %s, want a status for request 1", lines[1])
	}

	// nylon doesn't serve WireGuard's UAPI, so the wg tool can't see or change its transport.
	if stdout, _, err := h.Exec("node1", []string{"test", "-e", "/var/run/wireguard/nylon0.sock"}); err == nil {
		t.Fatalf("WireGuard UAPI socket exists:\n%s", stdout)
	}
	if stdout, _, err := h.Exec("node1", []string{"wg", "show", "nylon0"}); err == nil {
		t.Fatalf("wg can talk to nylon:\n%s", stdout)
	}
}
