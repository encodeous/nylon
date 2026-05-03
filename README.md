<div align="center">
    <img src="docs/assets/banner.svg" width=500 height=250 alt="nylon - a self-healing mesh network built on WireGuard">

[![Join our Discord](https://img.shields.io/discord/1499576745916104795?logo=discord&style=for-the-badge)](https://discord.gg/987gqqPGqr)
[![Docs](https://img.shields.io/badge/docs-nylon.jq.ax-blue?style=for-the-badge)](https://nylon.jq.ax)

</div>

---

# nylon

Nylon is a [Resilient Overlay Network](https://dl.acm.org/doi/10.1145/502034.502048) built from WireGuard, designed to be performant, secure, reliable, and easy to use.

### Main Features
- **Dynamic Routing**: nylon does not require all nodes to be reachable from each other, unlike mesh-based VPN projects (e.g Tailscale, Nebula, ZeroTier and Innernet)
- **Ease of Deployment**: nylon runs on a single UDP port (`57175`), is distributed by a single statically-linked binary, and is configured by a single configuration file.
- **WireGuard Backwards Compatibility**: connect existing WireGuard clients to a nylon network with no extra software. Useful for mobile clients.

## Getting Started

Download the latest release binary from the [releases page](https://github.com/encodeous/nylon/releases), then head to the [docs](https://nylon.jq.ax) for setup instructions.

Sample systemd service and launchctl plist files can be found under the `examples` directory.

> [!NOTE]
> - I daily-drive the Linux and macOS versions, but the Windows client currently has issues. I recommend using [WireGuard for Windows](https://www.wireguard.com/install/) and connecting to a Linux/macOS machine as a passive client.
> - Nylon is early stage software, so expect (some) bugs, breaking changes, and unaudited code.
>   - That said, nylon does not modify WireGuard's cryptographic code
>   - All nylon packets are sent within the WireGuard tunnel.
> - Feel free to report bugs and suggest features via GitHub issues. For security concerns, [contact me directly](https://jiaqi.ch/).

---

Built with sweat and tears (thankfully no blood)

`nylon` is not an official WireGuard project, and WireGuard is a registered trademark of Jason A. Donenfeld.
