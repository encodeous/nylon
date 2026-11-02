"""One fixed topology: win1 <-> linux <-> win2, on an Ubuntu/KVM host."""

import functools
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time
import unittest

import winrm


IMAGE = Path(sys.argv.pop(1)).resolve()
WORK = Path("workdir/e2e/windows").resolve()
NETWORK = "nylon-windows-e2e"
BRIDGE = "nylon-e2e"
LINUX = "nylon-e2e-linux"


def run(*args, **kwargs):
    return subprocess.run(args, check=True, capture_output=True, text=True, **kwargs).stdout.strip()


def host_network(script):
    run("docker", "run", "--rm", "--privileged", "--network", "host",
        "--entrypoint", "sh", "nylon-debug:latest", "-ec", script)


def powershell(port, script):
    # Each operation owns its WinRM session and closes the HTTP connection.
    session = winrm.Session(f"http://127.0.0.1:{port}/wsman", auth=("Administrator", "nylon"),
                            transport="ntlm", operation_timeout_sec=10, read_timeout_sec=20)
    try:
        result = session.run_ps("$ErrorActionPreference = 'Stop';\n"
                                "[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false);\n" + script)
        if result.status_code:
            raise RuntimeError(result.std_out.decode() + result.std_err.decode())
        return result.std_out.decode().strip()
    finally:
        session.protocol.transport.close_session()


def wait_for(description, operation, timeout):
    deadline = time.monotonic() + timeout
    while True:
        try:
            return operation()
        except Exception as error:
            if time.monotonic() >= deadline:
                raise TimeoutError(f"Waiting for {description}: {error}") from error
            time.sleep(1)


class WindowsConnectivity(unittest.TestCase):
    def start_windows(self, index):
        name, port, tap = f"win{index}", 55984 + index, f"nylon-win{index}"
        directory = WORK / name
        directory.mkdir(exist_ok=True)
        disk, variables = directory / "disk.qcow2", directory / "vars.fd"
        run("/usr/bin/qemu-img", "create", "-f", "qcow2", "-F", "qcow2", "-b", str(IMAGE), str(disk))
        self.addCleanup(disk.unlink)
        shutil.copyfile("/usr/share/OVMF/OVMF_VARS_4M.fd", variables)
        self.addCleanup(variables.unlink)
        host_network(f"ip tuntap add {tap} mode tap user {os.getuid()}; "
                     f"ip link set {tap} master {BRIDGE}; ip link set {tap} up")
        self.addCleanup(host_network, f"ip link delete {tap}")
        log = (directory / "qemu.log").open("w")
        self.addCleanup(log.close)
        process = subprocess.Popen([
            "/usr/bin/qemu-system-x86_64", "-machine", "q35", "-accel", "kvm", "-cpu", "host",
            "-m", "2048", "-smp", "2", "-display", "none", "-monitor", "none",
            "-serial", f"file:{directory / 'serial.log'}",
            "-drive", "if=pflash,format=raw,readonly=on,file=/usr/share/OVMF/OVMF_CODE_4M.fd",
            "-drive", f"if=pflash,format=raw,file={variables}",
            "-drive", f"if=ide,format=qcow2,file={disk}",
            "-netdev", f"tap,id=data,ifname={tap},script=no,downscript=no",
            "-device", f"virtio-net-pci,netdev=data,mac=52:54:00:00:00:0{index}",
            "-netdev", f"user,id=mgmt,restrict=on,net=192.168.252.0/24,hostfwd=tcp:127.0.0.1:{port}-:5985",
            "-device", "virtio-net-pci,netdev=mgmt,mac=52:54:00:12:34:56",
        ], stdout=log, stderr=log)

        def stop():
            process.kill()
            process.wait()

        self.addCleanup(stop)

        def configure():
            if process.poll() is not None:
                self.fail(f"{name} QEMU exited; see {directory / 'qemu.log'}")
            return powershell(port, f"""
$adapter = Get-NetAdapter | Where-Object {{ $_.MacAddress -eq '52-54-00-00-00-0{index}' }}
Set-NetIPInterface -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -Dhcp Disabled
Get-NetIPAddress -InterfaceIndex $adapter.ifIndex -AddressFamily IPv4 -ErrorAction SilentlyContinue | Remove-NetIPAddress -Confirm:$false
New-NetIPAddress -InterfaceIndex $adapter.ifIndex -IPAddress '172.30.250.{8 + index * 2}' -PrefixLength 24 | Out-Null
Set-NetFirewallProfile -Profile Domain,Private,Public -Enabled False
[void][IO.Directory]::CreateDirectory('C:\\nylon-e2e')
""")
        print(f"Waiting for {name} WinRM and network setup...", flush=True)
        wait_for(f"{name} network setup", configure, 300)
        return port

    def test_connectivity(self):
        run("docker", "network", "create", "--subnet", "172.30.250.0/24", "--ip-range", "172.30.250.128/25",
            "--gateway", "172.30.250.1", "-o", f"com.docker.network.bridge.name={BRIDGE}", NETWORK)
        self.addCleanup(run, "docker", "network", "rm", NETWORK)

        central = {"routers": [], "graph": ["win1, linux", "linux, win2"], "timestamp": time.time_ns()}
        for name, address in [("win1", "10.0.0.1"), ("linux", "10.0.0.2"), ("win2", "10.0.0.3")]:
            key = run("docker", "run", "--rm", "--entrypoint", "nylon", "nylon-debug:latest", "genkey")
            public = run("docker", "run", "--rm", "-i", "--entrypoint", "nylon", "nylon-debug:latest",
                         "pubkey", input=key + "\n")
            central["routers"].append({"id": name, "pubkey": public, "addresses": [address],
                                       "endpoints": ["172.30.250.11:57175"] if name == "linux" else []})
            local = {"id": name, "key": key, "port": 57175, "interface_name": "nylon0"}
            if name != "linux":
                local["log_path"] = "C:\\nylon-e2e\\nylon.log"
            (WORK / f"{name}.json").write_text(json.dumps(local))
        (WORK / "central.json").write_text(json.dumps(central))

        run("docker", "run", "-d", "--name", LINUX, "--privileged", "--network", NETWORK,
            "--ip", "172.30.250.11", "-v", f"{WORK}:/app/config:ro", "nylon-debug:latest",
            "--config", "central.json", "--node", "linux.json")
        self.addCleanup(run, "docker", "rm", "-f", LINUX)

        handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(WORK))
        server = http.server.ThreadingHTTPServer(("172.30.250.1", 0), handler)
        self.addCleanup(server.server_close)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.addCleanup(server.shutdown)
        url = f"http://172.30.250.1:{server.server_port}"

        ports = {}
        for index in (1, 2):
            name = f"win{index}"
            port = ports[name] = self.start_windows(index)
            powershell(port, f"""
foreach ($file in @('nylon.exe', 'wintun.dll', 'central.json', '{name}.json')) {{
  Invoke-WebRequest -UseBasicParsing ('{url}/' + $file) -OutFile ('C:\\nylon-e2e\\' + $file)
}}
# SYSTEM provides UAPI privileges and the scheduled task survives closing WinRM.
$action = New-ScheduledTaskAction -Execute 'C:\\nylon-e2e\\nylon.exe' -WorkingDirectory 'C:\\nylon-e2e' -Argument 'run -v -w --config central.json --node {name}.json'
$principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -TaskName 'nylon-e2e' -Action $action -Principal $principal | Out-Null
Start-ScheduledTask -TaskName 'nylon-e2e'
""")

        def collect_logs():
            (WORK / "linux.log").write_text(run("docker", "logs", LINUX))
            for name, port in ports.items():
                try:
                    (WORK / name / "nylon.log").write_text(powershell(port,
                        "Get-Content 'C:\\nylon-e2e\\nylon.log' -Raw -Encoding UTF8"))
                except Exception as error:
                    print(f"Reading {name} logs: {error}", flush=True)

        self.addCleanup(collect_logs)

        def ping(source, destination, count):
            if source == "linux":
                output = run("docker", "exec", LINUX, "ping", "-n", "-c", str(count), "-W", "2", destination)
                self.assertIn(f"{count} packets transmitted, {count} received", output)
                return output
            return powershell(ports[source], f"""
$ping = New-Object System.Net.NetworkInformation.Ping
for ($i = 0; $i -lt {count}; $i++) {{
  $reply = $ping.Send('{destination}', 2000)
  if ($reply.Status -ne [System.Net.NetworkInformation.IPStatus]::Success) {{ throw $reply.Status }}
  $reply.Status
}}
""")

        for source, destination in [("win1", "10.0.0.3"), ("win2", "10.0.0.1"),
                                    ("win1", "10.0.0.2"), ("linux", "10.0.0.1")]:
            with self.subTest(source=source, destination=destination):
                wait_for(f"{source} -> {destination} convergence", lambda: ping(source, destination, 1), 120)
                print(f"{source} -> {destination}: {ping(source, destination, 3)}", flush=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
