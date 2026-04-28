---
title: Passive Clients
description: Connecting standard WireGuard clients to a Nylon network.
---

One of Nylon's best features is compatibility with standard WireGuard clients (iOS, Android, Windows, etc.). These are called **Passive Nodes**.

## How it Works

Passive nodes do not participate in the routing protocol. Instead, they connect to a **Gateway Node** (a regular Nylon node) which advertises their presence to the rest of the network.

## 1. Configure the Passive Client

Use any WireGuard app to generate a keypair. Note the public key.

## 2. Update Central Configuration

Add the passive client to your `central.yaml`. Passive clients should **not** have endpoints.

```yaml
nodes:
  - id: my-phone
    pubkey: <PHONE_PUBLIC_KEY>
    addresses:
      - 10.0.0.5/32 # Use /32 for individual clients
```

## 3. Connect to a Gateway

In your WireGuard app, set the `Endpoint` to the address of any Nylon node in your network that has a public endpoint.

```ini
[Interface]
PrivateKey = <PHONE_PRIVATE_KEY>
Address = 10.0.0.5/32

[Peer]
PublicKey = <GATEWAY_NODE_PUBLIC_KEY>
Endpoint = gateway.example.com:57175
AllowedIPs = 10.0.0.0/24
```

## Limitations

- Passive clients can only connect to one node at a time.
- They cannot forward traffic for other nodes.
- They rely on the gateway node for connectivity to the rest of the network.
