//go:build linux

package machine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/masterzen/winrm"
	"github.com/masterzen/winrm/soap"
)

// Each SOAP request owns its NTLM connection. WinRM sends and long-poll
// receives can run concurrently without sharing an encrypted session or pool.
// Closing the socket also interrupts library calls that do not forward ctx.
type winRMTransport struct {
	ctx      context.Context
	endpoint *winrm.Endpoint
}

func (t *winRMTransport) Transport(endpoint *winrm.Endpoint) error {
	t.endpoint = endpoint
	return nil
}

func (t *winRMTransport) Post(client *winrm.Client, message *soap.SoapMessage) (string, error) {
	address := net.JoinHostPort(t.endpoint.Host, strconv.Itoa(t.endpoint.Port))
	conn, err := (&net.Dialer{Timeout: 20 * time.Second}).DialContext(t.ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	stop := context.AfterFunc(t.ctx, func() { _ = conn.Close() })
	defer stop()
	transport := winrm.NewClientNTLMWithDial(func(string, string) (net.Conn, error) { return conn, nil })
	if err := transport.Transport(t.endpoint); err != nil {
		return "", err
	}
	return transport.Post(client, message)
}

// runPS uses an encoded command, so scripts never pass through cmd.exe quoting.
func (w *QEMUWindows) runPS(ctx context.Context, script string, input string) (Result, error) {
	script = `$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$global:LASTEXITCODE = 0
` + script + `
exit $LASTEXITCODE`
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	params := *winrm.DefaultParameters
	params.Timeout = "PT10S"
	params.TransportDecorator = func() winrm.Transporter { return &winRMTransport{ctx: ctx} }
	endpoint := winrm.NewEndpoint("127.0.0.1", w.port, false, false, nil, nil, nil, 20*time.Second)
	client, err := winrm.NewClientWithParameters(endpoint, "Administrator", "nylon", &params)
	if err != nil {
		return Result{}, err
	}
	stdout, stderr, code, err := client.RunPSWithContextWithString(ctx, script, input)
	return (Result{Stdout: stdout, Stderr: stderr, ExitCode: code}).check(err)
}

func (w *QEMUWindows) Run(ctx context.Context, cmd Command) (Result, error) {
	args := []string{psQuote(cmd.Path)}
	for _, arg := range cmd.Args {
		args = append(args, psQuote(arg))
	}
	return w.runPS(ctx, "& "+strings.Join(args, " "), "")
}

// WriteFile sends base64 lines through one WinRM shell. This avoids a separate
// PowerShell/WinRM session for every small chunk of the Nylon executable.
func (w *QEMUWindows) WriteFile(ctx context.Context, path string, data []byte) error {
	expected := fmt.Sprintf("%X", sha256.Sum256(data))
	var input bytes.Buffer
	for len(data) != 0 {
		n := min(len(data), 36*1024)
		input.WriteString(base64.StdEncoding.EncodeToString(data[:n]))
		input.WriteByte('\n')
		data = data[n:]
	}
	script := `$path = ` + psQuote(path) + `
$file = [IO.File]::Create($path)
while ($null -ne ($line = [Console]::ReadLine())) {
  $bytes = [Convert]::FromBase64String($line)
  $file.Write($bytes, 0, $bytes.Length)
}
$file.Close()
(Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash`
	r, err := w.runPS(ctx, script, input.String())
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Stdout) != expected {
		return fmt.Errorf("WinRM upload checksum mismatch for %s", path)
	}
	return nil
}

// A scheduled SYSTEM task detaches the process from WinRM's job object. A plain
// Start-Process inside a remote shell would be terminated when that shell closes.
// SYSTEM also gives Nylon the privileges required by its Windows UAPI listener.
func (w *QEMUWindows) Start(ctx context.Context, cmd Command) (Process, error) {
	runner := `$ErrorActionPreference = 'Stop'
$p = Start-Process -FilePath ` + psQuote(cmd.Path) + ` -ArgumentList ` + psQuote(strings.Join(cmd.Args, " ")) + ` -WorkingDirectory 'C:\nylon-e2e' -RedirectStandardOutput 'C:\nylon-e2e\stdout' -RedirectStandardError 'C:\nylon-e2e\stderr' -PassThru
$handle = $p.Handle
[IO.File]::WriteAllText('C:\nylon-e2e\pid', [string]$p.Id)
$p.WaitForExit()
[IO.File]::WriteAllText('C:\nylon-e2e\exit', [string]$p.ExitCode)`
	if err := w.WriteFile(ctx, `C:\nylon-e2e\run.ps1`, []byte(runner)); err != nil {
		return nil, err
	}
	script := `[IO.File]::WriteAllText('C:\nylon-e2e\stdout', '')
[IO.File]::WriteAllText('C:\nylon-e2e\stderr', '')
$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File C:\nylon-e2e\run.ps1'
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName 'nylon-e2e' -Action $action -Principal $principal | Out-Null
Start-ScheduledTask -TaskName 'nylon-e2e'`
	if _, err := w.runPS(ctx, script, ""); err != nil {
		return nil, err
	}
	return &windowsProcess{machine: w}, nil
}

type windowsProcess struct{ machine *QEMUWindows }

func (p *windowsProcess) Poll(ctx context.Context) (Result, bool, error) {
	script := `function Read-SharedFile($path) {
  $file = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
  $memory = New-Object IO.MemoryStream
  $file.CopyTo($memory)
  $file.Close()
  [Convert]::ToBase64String($memory.ToArray())
}
$code = $null
if (Test-Path 'C:\nylon-e2e\exit') { $code = [int][IO.File]::ReadAllText('C:\nylon-e2e\exit') }
@{stdout = (Read-SharedFile 'C:\nylon-e2e\stdout'); stderr = (Read-SharedFile 'C:\nylon-e2e\stderr'); code = $code} | ConvertTo-Json -Compress`
	r, err := p.machine.runPS(ctx, script, "")
	if err != nil {
		return Result{}, false, err
	}
	return processOutput(r.Stdout)
}

func (p *windowsProcess) Stop(ctx context.Context) error {
	_, err := p.machine.runPS(ctx, `Stop-Process -Id ([int][IO.File]::ReadAllText('C:\nylon-e2e\pid')) -Force
Unregister-ScheduledTask -TaskName 'nylon-e2e' -Confirm:$false
[IO.File]::WriteAllText('C:\nylon-e2e\exit', '-1')`, "")
	return err
}
