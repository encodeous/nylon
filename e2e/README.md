# Portable e2e machines

`StartMachines`, `machine.RunNylon` and `Harness.Ping` run the same scenarios on
Linux Docker containers and QEMU Windows guests. `NodeSpec.OS` selects individual
nodes; `NYLON_E2E_MACHINE` selects the default. Auxiliary services and unmigrated
Linux tests keep using `StartNodes`.

```sh
NYLON_E2E_MACHINE=windows go test -tags=e2e ./e2e \
  -run '^(TestMachines|TestConnectivity)$' -count=1 -parallel=1 -timeout=20m -v
NYLON_E2E_MACHINE=mixed go test -tags=e2e ./e2e \
  -run '^TestConnectivity$' -count=1 -parallel=1 -timeout=20m -v
```

The runner is Linux with a local rootful Docker daemon/CLI, writable `/dev/kvm`,
`/dev/net/tun`, `qemu-system-x86_64`, `qemu-img`, and Ubuntu's OVMF 4M firmware.
On Ubuntu install `qemu-system-x86 qemu-utils ovmf`. Guests use fixed settings:
Windows Server 2025, UEFI, virtio NICs, 2 CPUs, 2 GiB RAM and WinRM HTTP/NTLM on
port 5985 as `Administrator` / `nylon`. The image includes the NIC drivers.

Preflight downloads `nylon-ci-windows-image/nylon-ci-win2025.qcow2.zst` from R2,
builds Windows amd64 Nylon, and prepares Wintun 0.14.1 once before parallel boot.
R2 credentials are `AWS_ENDPOINT_URL_S3`, `AWS_ACCESS_KEY_ID`, and
`AWS_SECRET_ACCESS_KEY`, supplied as environment variables or in the ignored root
`.env`. The image is cached in `workdir/e2e/images`; clear it to replace the image.
Each VM uses a new qcow2 overlay and UEFI variables; cleanup removes both and
retains diagnostics under `workdir/e2e/{vms,logs}`.

A TAP joins each data NIC to the scenario's `/24` Docker bridge. Docker assigns
addresses from `.128/25`; nodes use explicit addresses in `.2`–`.127`. A separate
restricted management NIC forwards WinRM to localhost and cannot carry test
traffic. Disposable guests have their firewall disabled. Each machine runs one
Nylon process, launched as a SYSTEM task on Windows; uploads use SHA256 checks.

Windows installs selected prefixes separately to avoid merging adjacent hosts
into on-link subnets with broadcast addresses. Linux/macOS retain aggregation;
explicitly advertised subnets remain subnet routes.

CI workflow integration will follow separately once Vault credentials are ready.
Allow 4 GiB of guest RAM for two Windows nodes or 6 GiB for three.
