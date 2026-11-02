# Windows e2e

Run on x86_64 Ubuntu with Docker, KVM and Go installed:

```sh
sudo apt-get install qemu-system-x86 qemu-utils ovmf python3-venv unzip curl
# The current user must have read/write access to /dev/kvm and the Docker socket.
e2e/windows/run.sh /absolute/path/to/windows.qcow2
```

The script builds Nylon for Linux and Windows, downloads Wintun, and installs
pywinrm in a local virtualenv. QEMU uses `/usr/bin/qemu-system-x86_64`,
`/usr/bin/qemu-img`, and `/usr/share/OVMF/OVMF_{CODE,VARS}_4M.fd`.

The image must support UEFI, virtio networking, and WinRM with
`Administrator` / `nylon`. Each VM uses a fresh qcow2 overlay and UEFI variables;
the supplied backing image is unchanged. Guest firewalls are disabled.

There is one test: `win1 <-> linux <-> win2`. It checks three consecutive ping
replies in both directions between the Windows nodes, and between win1 and Linux.
The Docker network and WinRM ports are fixed; run only one instance per host.
Logs stay in `workdir/e2e/windows`; VMs, overlays, TAPs and containers are removed.

CI builds `github:starcatmeow/nylon-ci-windows-image/main#nylon-ci-win2025`,
decompresses its output, and passes the qcow2 path to the same script.
CI reads the public Nix cache at
`https://pub-b7e688bb852e422d84f49c2dce8e3461.r2.dev`, trusting the
`nylon-cache-1` public key configured in the workflow. Cache misses or download
failures fall back to local builds. No cache credentials or uploads are needed;
publish the signed final image output to the cache separately.
