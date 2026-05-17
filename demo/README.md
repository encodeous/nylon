# Demo

Generates a GIF showing nylon's self-healing failover in a 3-node mesh.

The script spins up alice, bob, and charlie in Docker, cuts the direct alice-charlie link with iptables, and records nylon rerouting traffic through bob. Then it restores the link and shows nylon switching back.

## Requirements

- Docker with compose v2
- python3

## Usage

```
cd demo
./record.sh
```

Output: `output/demo.gif`

Takes about 2 minutes (image build + mesh convergence + recording).
