//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/encodeous/nylon/state"
)

func TestIPCRawSocketJSONHasNoErrnoTrailer(t *testing.T) {
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

	stdout, _, err := h.Exec("node1", []string{
		"python3",
		"-c",
		`import socket
s=socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.connect("/var/run/wireguard/nylon0.sock")
s.sendall(b'get=nylon\n{"status":{}}\n')
line=s.recv(65535).decode()
print(line, end="")
s.settimeout(0.2)
try:
    extra=s.recv(65535).decode()
    print(extra, end="")
except TimeoutError:
    pass
s.close()
`,
	})
	if err != nil {
		t.Fatalf("raw IPC request failed: %v\nstdout:\n%s", err, stdout)
	}
	if strings.Contains(stdout, "errno=") {
		t.Fatalf("raw IPC response included UAPI errno trailer:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"ok":true`) {
		t.Fatalf("raw IPC response did not include a successful protojson response:\n%s", stdout)
	}
}
