---
title: CLI Reference
description: Command-line interface documentation for Nylon.
---

The `nylon` command is the primary way to interact with your network.

## Common Commands

### `nylon run`

Starts the Nylon daemon.

| Flag | Short | Description |
| :--- | :--- | :--- |
| `--config` | `-c` | Path to `central.yaml`. |
| `--node` | `-n` | Path to `node.yaml`. |
| `--verbose` | `-v` | Enable verbose output. |
| `--json` | `-j` | Enable structured JSON logging. |

### `nylon key`

Generates a new WireGuard keypair.

- **Stdout**: Private key.
- **Stderr**: Public key.

### `nylon inspect` (alias: `i`)

Shows the current status of a running Nylon interface.

```bash
nylon inspect nylon
```

- `-t, --trace`: Enables live packet routing capture, showing how packets are being forwarded through the overlay.

## Troubleshooting Flags

These flags can be used with `nylon run` to diagnose issues:

- `--dbg-wg`: Output internal WireGuard logs.
- `--dbg-trace-tc`: Log every packet routing decision.
- `--dbg-perf`: Start a performance profiling server on port 6060.
