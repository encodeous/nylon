# Polyamide

Packet transports between Nylon peers.

- The core owns TUN I/O, routing, and the filter chain.
- Transports own sockets, encryption, and peer state.
- Filters return decisions. Batch dispatch groups packets by transport.
- The WireGuard backend is in [adapter](transports/wireguard/adapter).

Runtime:

- `core.Runtime` supplies transports and a host device.
- `Runtime.LinksForNeighbour` supplies links across registered transport instances.
- Routes select a neighbour. Link selection chooses the peer and native endpoint.
- YAML configuration uses WireGuard.

See [api.go](api.go) for the interfaces and [traffic_control.go](traffic_control.go)
for filtering and batch dispatch.
