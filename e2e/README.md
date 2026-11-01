# End-to-end machines

The Linux test runner can launch Nylon in Linux Docker containers or Windows
QEMU VMs. Use `StartMachines`, `machine.RunNylon` and `Harness.Ping` for portable
scenarios; existing tests and auxiliary services can keep using `StartNodes`.
`NodeSpec.OS` overrides `NYLON_E2E_MACHINE` for an individual node.

```sh
# Existing Linux suite
mise run e2e

# Windows startup, IPC, ping, process shutdown and three-node forwarding
NYLON_E2E_MACHINE=windows go test -tags=e2e ./e2e \
  -run '^(TestMachines|TestConnectivity)$' -count=1 -parallel=1 -timeout=20m -v

# Linux endpoints and a Windows relay
NYLON_E2E_MACHINE=mixed go test -tags=e2e ./e2e \
  -run '^TestConnectivity$' -count=1 -parallel=1 -timeout=20m -v

# Unit tests, without Docker or booting Windows
go test -race ./e2e/machine
```

## Setup

Windows requires a local rootful Linux Docker daemon, `qemu-system-x86_64`,
`qemu-img`, `/dev/net/tun`, writable `/dev/kvm` and OVMF. On Ubuntu install
`qemu-system-x86 qemu-utils ovmf`. Privileged Docker helpers manage TAPs;
QEMU runs as the test user. The image must boot with UEFI, have virtio NIC
and DHCP support, and expose WinRM HTTP/NTLM on port 5985.
The supplied image uses `Administrator` / `nylon` as disposable test credentials.

Preflight downloads and validates the base image, builds Windows amd64 Nylon,
and prepares Wintun 0.14.1 once per test process, before machines start in
parallel. R2 credentials come from the ignored root `.env`; exported variables
win: `AWS_ENDPOINT_URL_S3`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`.
The default object is `nylon-ci-windows-image/nylon-ci-win2025.qcow2.zst`.
Go handles signing and decompression; AWS CLI and zstd tools are not needed.

The read-only base is cached in `workdir/e2e/images` using the object filename
without `.zst`. Failed downloads are not published. Clear the cache when
replacing the image; simultaneous test processes sharing this cache are not
supported. Each VM gets a fresh qcow2 backed by that image and its own UEFI vars.
Cleanup removes QEMU, TAPs, overlays and vars while preserving the base and
host/guest logs under `workdir/e2e/{vms,logs}`.

WinRM uploads the binaries and YAML. Each operation creates an independent
client; each SOAP request owns a temporary encrypted NTLM connection. SYSTEM
scheduled tasks keep Nylon alive after the WinRM shell closes and provide UAPI
permissions. Uploads are checked with SHA256.

## Networking

Each scenario has a Docker bridge and `/24`. VM TAPs join that bridge; Docker
allocates dynamic addresses from `.128/25`, while explicit node IPs use `.2`–`.127`.
Each VM also has a restricted management NIC with localhost-only WinRM forwarding;
it cannot carry test traffic through management NAT. The data NIC has no default
gateway. The firewall stays enabled, with allow rules on data and Nylon interfaces.
Docker DNS aliases do not extend to VMs: use service IPs or an explicit resolver.

Windows keeps selected route prefixes separate; Linux/macOS still merge them.
This avoids adjacent host addresses becoming an on-link subnet whose upper IP
Windows treats as broadcast. Explicitly advertised subnets remain subnet routes.

## Overrides

| Variable | Default / purpose |
| --- | --- |
| `NYLON_E2E_WINDOWS_IMAGE` | Existing standalone qcow2; bypass R2 |
| `NYLON_E2E_IMAGE_CACHE` | `workdir/e2e/images` |
| `NYLON_E2E_WINDOWS_BUCKET` / `NYLON_E2E_WINDOWS_OBJECT` | R2 bucket/object above |
| `NYLON_E2E_WINRM_USERNAME` / `NYLON_E2E_WINRM_PASSWORD` | `Administrator` / `nylon` |
| `NYLON_E2E_WINDOWS_MEMORY_MIB` / `NYLON_E2E_WINDOWS_CPUS` | `2048` / `2` per VM |
| `NYLON_E2E_QEMU_ACCEL` / `NYLON_E2E_QEMU_NIC` | `kvm` / `virtio-net-pci`; `tcg` is opt-in |
| `NYLON_E2E_UEFI_CODE` / `NYLON_E2E_UEFI_VARS` | `/usr/share/OVMF/OVMF_{CODE,VARS}_4M.fd` |
| `NYLON_E2E_WINDOWS_BINARY` / `NYLON_E2E_WINTUN_DLL` | Existing Windows amd64 binaries |

## CI

CI integration will be added separately once Vault credentials are configured.
The local commands above can be used on a Linux runner with Docker and KVM.
Allow 4 GiB of guest RAM for two Windows nodes or 6 GiB for three.
