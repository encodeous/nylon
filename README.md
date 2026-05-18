<div align="center">
    <img src="docs/assets/banner.svg" width=500 height=250 alt="nylon - a self-healing mesh network built on WireGuard">

[![Join our Discord](https://img.shields.io/discord/1499576745916104795?logo=discord&style=for-the-badge)](https://discord.gg/987gqqPGqr)
[![Docs](https://img.shields.io/badge/docs-nylon.jq.ax-blue?style=for-the-badge)](https://nylon.jq.ax)

</div>

---

# nylon

Nylon is a self-healing WireGuard mesh that routes around failures. If a link goes down, nylon reroutes traffic through the next-best path in seconds. No manual intervention, no central coordination servers, just like how a real network should be :)

Under the hood, nylon implements the [Babel routing protocol (RFC 8966)](https://datatracker.ietf.org/doc/html/rfc8966) on top of a [modified wireguard-go](https://github.com/encodeous/nylon/tree/main/polyamide), using measured latency as the routing metric. 

Nylon targets under 10 seconds of convergence time after a link failure, as you can see in the demo below.

![Demo](docs/assets/demo.gif)

### Main Features
- **Multi-hop Routing**: traffic flows through the lowest-latency path across your mesh. Unlike Tailscale, Nebula, or ZeroTier, nodes don't need to be directly reachable from each other. Nylon forwards through intermediate hops automatically.
- **No Coordination Server**: no SaaS dependency, no single control-plane. Nodes exchange routes directly over the same WireGuard tunnel that carries your data.
- **Single Binary, Single Port**: one statically-linked binary, one UDP port (`57175`), one YAML config. That's it.
- **WireGuard Client Compatibility**: connect stock WireGuard clients (iOS, Android, Windows) to the mesh with zero extra software. Mobile clients roam between gateways seamlessly.
- **Native WireGuard Speeds**: data-plane runs entirely in `wireguard-go` (polyamide), capable of 10+ Gbps throughput.

## Getting Started

Download the latest release binary from the [releases page](https://github.com/encodeous/nylon/releases), then head to the [docs](https://nylon.jq.ax) for setup instructions.

> **[Read the full documentation at nylon.jq.ax](https://nylon.jq.ax)**
> includes configuration reference, guides for connecting WireGuard clients, port forwarding, and comparisons with Tailscale/Nebula.

Sample systemd service and launchctl plist files can be found under the `examples` directory.

> [!NOTE]
> **Stability:** I daily-drive nylon on Linux and macOS. The routing protocol has an [extensive test suite](https://github.com/encodeous/nylon/blob/main/core/router_test.go) and integration tests with simulated network conditions. The config format may still change between releases.
>
> **Security:** Nylon does not modify WireGuard's cryptographic code. All nylon control traffic (route updates, probes) is sent inside the encrypted WireGuard tunnel. For security concerns, [contact me directly](https://jiaqi.ch/).
>
> **Windows:** The Windows TUN interface has known issues. For now, I recommend connecting Windows machines as [passive WireGuard clients](/guides/wg-clients) via a Linux/macOS gateway.
>
> Bugs and feature requests welcome via [GitHub issues](https://github.com/encodeous/nylon/issues).

---

Built with sweat and tears (thankfully no blood)

`nylon` is not an official WireGuard project, and WireGuard is a registered trademark of Jason A. Donenfeld.
