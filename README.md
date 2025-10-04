# Nylon

Nylon is a [Resilient Overlay Network](https://dl.acm.org/doi/10.1145/502034.502048) forked from WireGuard, designed to be performant, secure, reliable, and most importantly, easy to use.

<details>

<summary>What is a Resilient Overlay Network?</summary>

A Resilient Overlay Network (RON) is an architecture that allows distributed applications to detect and recover from path outages and periods of degraded performance. It is an application-level overlay built on top of the existing Internet infrastructure, and it can be used to improve the reliability and performance of applications by routing traffic through intermediate nodes in the overlay network.

</details>

<details>

<summary>Technical Overview</summary>

Nylon is the integration of the Babel routing protocol with Polyamide (an advanced fork of WireGuard-go that enables routing).

### Polyamide

Polyamide is a fork of WireGuard-go that offers two notable features which enable dynamic routing:
- **Code-Defined Packet Manipulation and Redirection**: Polyamide can be configured to forward packets between its peers, and manipulate packets in transit (e.g decrementing the TTL). This is achieved completely in user-space without the need for modifying kernel routing tables.
- **Multi-endpoint Support**: Polyamide can maintain multiple endpoints for a single peer, allowing the control plane to dynamically select the best endpoint for a peer, and to send control messages over multiple physical links.

### Routing

Nylon closely implements the [Babel](https://datatracker.ietf.org/doc/html/rfc8966) routing protocol, a distance-vector routing protocol that is robust and efficient in both wireless mesh networks and wired networks. (However, Nylon is not compatible with existing Babel implementations due to fundamental differences) The main implementation can be found in [core/router_algo.go](core/router_algo.go).

Here are some key points about Nylon's routing protocol:
- Nylon uses in-band control messages to exchange routing information between nodes. These messages are sent over the same WireGuard tunnels used for data traffic, ensuring that routing information is not leaked. This is achieved by using Polyamide's code-defined packet manipulation to generate a pseudo "IPv8" header (as defined by `NyProtoId` in [polyamide/device/traffic_manip.go](polyamide/device/traffic_manip.go).
- Nylon maintains backwards-compatibility with vanilla WireGuard clients by treating them as leaf nodes that do not participate in routing. These "passive" nodes must attach to a "gateway" (Nylon) node that advertises their presence on the network.
- Nylon uses a statistic-based hysteresis function to prevent frequent route switching. This is particularly important in overlay networks where the underlying physical network may be unstable (as defined in [state/endpoint.go](state/endpoint.go)).

</details>

### Main Features
- **Dynamic Routing**: Unlike other mesh-based VPN projects (e.g Tailscale, Nebula, and ZeroTier), Nylon does not require all nodes to be reachable from each other. As long as there exists a path (even through intermediate nodes), Nylon will find it and route packets according to the most optimal (latency-wise) path. _Sometimes, it may be the case that the most optimal path is [not the most direct path](https://www.cloudflare.com/en-ca/learning/performance/routing-vs-smart-routing/)!_
- **Central Configuration**: Nylon uses a central configuration file to define the network topology. This makes it easy to manage the network, as you only need to update the central configuration file and automatically distribute it to all nodes. _Note that this does not mean there is a central server, as all nodes are equal peers in the network._
- **Vanilla Client Support**: Nylon is fully compatible with vanilla WireGuard clients (e.g WireGuard for iOS/Android). This means you can use your existing WireGuard clients to connect to a Nylon network, and they will be able to communicate with other Nylon nodes. _However, vanilla clients have limited functionality and must connect through another Nylon node acting as a gateway._

# Getting Started

You can download the latest release binary from the [releases page](https://github.com/encodeous/nylon/releases). Sample systemd service and launchctl plist files can be found under the `examples` directory.

For a more in-depth example, please refer to the [example/README.md](example/README.md).

