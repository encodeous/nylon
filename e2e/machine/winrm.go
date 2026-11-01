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

	"github.com/google/uuid"
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
	if err := t.ctx.Err(); err != nil {
		return "", err
	}
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
	response, err := transport.Post(client, message)
	if t.ctx.Err() != nil {
		return "", t.ctx.Err()
	}
	return response, err
}

// runPS uses an encoded command, so scripts never pass through cmd.exe quoting.
func (w *QEMUWindows) runPS(ctx context.Context, script string, input string) (Result, error) {
	script = `$ErrorActionPreference = 'Stop'; $ProgressPreference = 'SilentlyContinue';
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$global:LASTEXITCODE = 0
try { ` + script + `
exit $LASTEXITCODE
} catch { [Console]::Error.WriteLine($_.ToString()); exit 1 }`
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	params := *winrm.DefaultParameters
	params.Timeout = "PT10S"
	params.TransportDecorator = func() winrm.Transporter { return &winRMTransport{ctx: ctx} }
	endpoint := winrm.NewEndpoint("127.0.0.1", w.port, false, false, nil, nil, nil, 20*time.Second)
	client, err := winrm.NewClientWithParameters(endpoint, w.cfg.Username, w.cfg.Password, &params)
	if err != nil {
		return Result{}, err
	}
	stdout, stderr, code, err := client.RunPSWithContextWithString(ctx, script, input)
	return (Result{Stdout: stdout, Stderr: stderr, ExitCode: code}).check(err)
}

// windowsArg implements the CommandLineToArgvW/C runtime quoting convention.
func windowsArg(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, c := range s {
		if c == '\\' {
			slashes++
			continue
		}
		if c == '"' {
			b.WriteString(strings.Repeat(`\`, slashes*2+1))
		} else {
			b.WriteString(strings.Repeat(`\`, slashes))
		}
		slashes = 0
		b.WriteRune(c)
	}
	b.WriteString(strings.Repeat(`\`, slashes*2))
	b.WriteByte('"')
	return b.String()
}

func windowsArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windowsArg(arg)
	}
	return strings.Join(quoted, " ")
}

func (w *QEMUWindows) Run(ctx context.Context, cmd Command) (Result, error) {
	if cmd.Path == "" {
		return Result{}, fmt.Errorf("empty executable")
	}
	script := `$si = New-Object System.Diagnostics.ProcessStartInfo
$si.UseShellExecute = $false
$si.RedirectStandardOutput = $true
$si.RedirectStandardError = $true
$si.CreateNoWindow = $true
$si.StandardOutputEncoding = New-Object System.Text.UTF8Encoding($false)
$si.StandardErrorEncoding = New-Object System.Text.UTF8Encoding($false)
$si.FileName = ` + psQuote(cmd.Path) + `
$si.Arguments = ` + psQuote(windowsArgs(cmd.Args)) + "\n"
	if cmd.Dir != "" {
		script += "$si.WorkingDirectory = " + psQuote(cmd.Dir) + "\n"
	}
	script += `$p = New-Object System.Diagnostics.Process
$p.StartInfo = $si
try {
  [void]$p.Start()
  $stdout = $p.StandardOutput.ReadToEndAsync()
  $stderr = $p.StandardError.ReadToEndAsync()
  $p.WaitForExit()
  [Console]::Out.Write($stdout.Result)
  [Console]::Error.Write($stderr.Result)
  exit $p.ExitCode
} finally { $p.Dispose() }`
	return w.runPS(ctx, script, "")
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
[void][IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($path))
$file = [IO.File]::Open($path, [IO.FileMode]::Create, [IO.FileAccess]::Write)
try {
  while ($null -ne ($line = [Console]::ReadLine())) {
    $bytes = [Convert]::FromBase64String($line)
    $file.Write($bytes, 0, $bytes.Length)
  }
} finally { $file.Dispose() }
$sha = [Security.Cryptography.SHA256]::Create()
$stream = [IO.File]::OpenRead($path)
try { [Console]::Out.Write([BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '')) }
finally { $stream.Dispose(); $sha.Dispose() }`
	r, err := w.runPS(ctx, script, input.String())
	if err != nil {
		return err
	}
	if strings.TrimSpace(r.Stdout) != expected {
		return fmt.Errorf("WinRM upload checksum mismatch for %s", path)
	}
	return nil
}

// Shared reads are necessary while the scheduled process is writing its logs.
const psReadShared = `function Read-SharedFile($path) {
  $file = [IO.File]::Open($path, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::ReadWrite)
  $memory = New-Object IO.MemoryStream
  try { $file.CopyTo($memory); return [Convert]::ToBase64String($memory.ToArray()) }
  finally { $file.Dispose(); $memory.Dispose() }
}
`

// A scheduled SYSTEM task detaches the process from WinRM's job object. A plain
// Start-Process inside a remote shell would be terminated when that shell closes.
// SYSTEM also gives Nylon the privileges required by its Windows UAPI listener.
func (w *QEMUWindows) Start(ctx context.Context, cmd Command) (Process, error) {
	if cmd.Path == "" {
		return nil, fmt.Errorf("empty executable")
	}
	task := "nylon-e2e-" + uuid.NewString()
	dir := guestPath(w, task)
	stdout, stderr := dir+`\stdout`, dir+`\stderr`
	workDir := cmd.Dir
	if workDir == "" {
		workDir = w.Info().WorkDir
	}
	argumentList := ""
	if len(cmd.Args) != 0 {
		argumentList = " -ArgumentList " + psQuote(windowsArgs(cmd.Args))
	}
	runner := `$ErrorActionPreference = 'Stop'
try {
  $p = Start-Process -FilePath ` + psQuote(cmd.Path) + argumentList + ` -WorkingDirectory ` + psQuote(workDir) + ` -RedirectStandardOutput ` + psQuote(stdout) + ` -RedirectStandardError ` + psQuote(stderr) + ` -PassThru
  $handle = $p.Handle
  [IO.File]::WriteAllText(` + psQuote(dir+`\pid`) + `, [string]$p.Id)
  $p.WaitForExit()
  [IO.File]::WriteAllText(` + psQuote(dir+`\exit`) + `, [string]$p.ExitCode)
} catch {
  [IO.File]::AppendAllText(` + psQuote(stderr) + `, $_.ToString())
  [IO.File]::WriteAllText(` + psQuote(dir+`\exit`) + `, '127')
}`
	if err := w.WriteFile(ctx, dir+`\run.ps1`, []byte(runner)); err != nil {
		return nil, err
	}
	script := `[IO.File]::WriteAllText(` + psQuote(stdout) + `, '')
[IO.File]::WriteAllText(` + psQuote(stderr) + `, '')
$action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument ` + psQuote(`-NoProfile -NonInteractive -ExecutionPolicy Bypass -File `+windowsArg(dir+`\run.ps1`)) + `
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName ` + psQuote(task) + ` -Action $action -Principal $principal -Settings $settings | Out-Null
Start-ScheduledTask -TaskName ` + psQuote(task) + `
for ($i = 0; $i -lt 150; $i++) {
  if ((Test-Path -LiteralPath ` + psQuote(dir+`\pid`) + `) -or (Test-Path -LiteralPath ` + psQuote(dir+`\exit`) + `)) { exit 0 }
  Start-Sleep -Milliseconds 100
}
throw 'Scheduled process did not start within 15 seconds'`
	if _, err := w.runPS(ctx, script, ""); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = (&windowsProcess{machine: w, dir: dir, task: task}).Stop(cleanupCtx)
		return nil, err
	}
	return &windowsProcess{machine: w, dir: dir, task: task}, nil
}

type windowsProcess struct {
	machine   *QEMUWindows
	dir, task string
}

func (p *windowsProcess) Poll(ctx context.Context) (Result, bool, error) {
	script := psReadShared + `$code = $null
if (Test-Path -LiteralPath ` + psQuote(p.dir+`\exit`) + `) { $code = [int][IO.File]::ReadAllText(` + psQuote(p.dir+`\exit`) + `) }
@{stdout = (Read-SharedFile ` + psQuote(p.dir+`\stdout`) + `); stderr = (Read-SharedFile ` + psQuote(p.dir+`\stderr`) + `); code = $code} | ConvertTo-Json -Compress`
	r, err := p.machine.runPS(ctx, script, "")
	if err != nil {
		return Result{}, false, err
	}
	return processOutput(r.Stdout)
}

func (p *windowsProcess) Stop(ctx context.Context) error {
	script := `if (!(Test-Path -LiteralPath ` + psQuote(p.dir+`\exit`) + `) -and (Test-Path -LiteralPath ` + psQuote(p.dir+`\pid`) + `)) {
  $child = [int][IO.File]::ReadAllText(` + psQuote(p.dir+`\pid`) + `)
  Stop-Process -Id $child -Force -ErrorAction SilentlyContinue
}
Stop-ScheduledTask -TaskName ` + psQuote(p.task) + ` -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName ` + psQuote(p.task) + ` -Confirm:$false -ErrorAction SilentlyContinue
if (!(Test-Path -LiteralPath ` + psQuote(p.dir+`\exit`) + `)) { [IO.File]::WriteAllText(` + psQuote(p.dir+`\exit`) + `, ` + psQuote(strconv.Itoa(-1)) + `) }`
	_, err := p.machine.runPS(ctx, script, "")
	return err
}
