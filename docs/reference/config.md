---
title: Configuration Reference
description: Detailed documentation for Nylon's local and central configuration files.
---

Nylon utilizes a dual-configuration system: **Local Configuration** (`node.yaml`) for node-specific settings and **Central Configuration** (`central.yaml`) for network-wide topology and shared settings.

---

## Local Configuration (`node.yaml`)

This file defines how an individual node behaves and identifies itself.

| Field | Type | Description | Default |
| :--- | :--- | :--- | :--- |
| `id` | `string` | A unique identifier for this node (must match `central.yaml`). | - |
| `key` | `string` | The WireGuard private key for this node. | - |
| `port` | `int` | The UDP port Nylon listens on for peer traffic. | `57175` |
| `interface_name` | `string` | The name of the TUN interface to create (e.g., `nylon`). | `nylon` / `utunX` |
| `use_system_routing`| `bool` | If true, all packets from peers will exit through the TUN interface. | `false` |
| `no_net_configure` | `bool` | If true, Nylon will not attempt to configure system networking/routes. | `false` |
| `dns_resolvers` | `[]string` | Custom DNS resolvers (e.g., `["1.1.1.1:53"]`) used for config fetching. | System Default |
| `log_path` | `string` | If set, Nylon will write logs to this file instead of stdout. | - |
| `exclude_ips` | `[]string` | CIDR ranges to exclude from the tunnel (adds to central exclusions). | `[]` |
| `unexclude_ips` | `[]string` | CIDR ranges to remove from the centrally excluded ranges. | `[]` |
| `dist` | `object` | Optional configuration for fetching central config automatically (see below). | - |
| `pre_up` / `post_up` | `[]string` | Commands to execute before/after the interface is brought up. | `[]` |
| `pre_down` / `post_down`| `[]string` | Commands to execute before/after the interface is brought down. | `[]` |

### `dist` (Local)
Used to bootstrap the central configuration from a remote source.
- `url`: The URL to the `.nybundle` file.
- `key`: The public key used to decrypt/verify the bundle.

---

## Central Configuration (`central.yaml`)

This file defines the entire network and must be identical on all nodes.

### `routers` and `clients`
Nodes are defined as either `routers` (active participants) or `clients` (passive participants).

| Field | Type | Description |
| :--- | :--- | :--- |
| `id` | `string` | Unique identifier. |
| `pubkey` | `string` | WireGuard public key. |
| `addresses` | `[]string` | Internal IP addresses (e.g., `10.0.0.1`) for this node. |
| `endpoints` | `[]string` | (Routers only) Publicly reachable addresses in `host:port` format. |
| `prefixes` | `[]object` | External prefixes advertised by this node (see Healthchecks). |

### `exclude_ips`
A list of CIDR ranges (e.g., `["192.168.1.0/24"]`) that should be excluded from the Nylon tunnel for all nodes. If empty, all advertised prefixes are included (Full Tunnel).

### `graph`
Defines the bidirectional links between nodes. Supports groups and topological expansion.
```yaml
graph:
  - "node1, node2"           # Connects node1 and node2
  - "GroupA = node1, node2"  # Defines a group
  - "GroupA, node3"          # Connects all nodes in GroupA to node3
```

---

## Advertised Prefixes & Healthchecks

Nylon can dynamically advertise routes based on the health of an external resource.

### Static Prefix
Always advertised with a fixed metric.
```yaml
prefixes:
  - type: static
    prefix: 10.10.0.0/24
    metric: 100
```

### Ping Healthcheck
Advertises the prefix as long as the target address is reachable via ICMP.
```yaml
prefixes:
  - type: ping
    prefix: 10.20.0.0/24
    addr: 10.20.0.1
    delay: 15s
    max_failures: 3
```

### HTTP Healthcheck
Advertises the prefix if an HTTP GET request to the URL returns a `200 OK`.
```yaml
prefixes:
  - type: http
    prefix: 10.30.0.0/24
    url: "http://internal-service.local/health"
    delay: 30s
```

---

## Global Distribution (`dist`)

Settings for the distribution system in `central.yaml`.

- `key`: The public key for the distribution repository.
- `repos`: A list of repository URLs.
