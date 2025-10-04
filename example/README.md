# Nylon Setup

#### Node Setup
On every node, copy the `sample-node.yaml`, and fill in the relevant details. run `./nylon key` to generate the keypair. The first key printed (stdout) is the private key, and the second key printed (stderr) is the public key. Fill the private key in the node config, and put the public key in the central config later.

#### Distribution
(Optional) To setup key distribution, you can also generate a keypair. Save the private key into `central.key` (this will be used by `nylon seal`). You can put the public key into the central config under `dist/key`.

> [!IMPORTANT]
> Do not share this **public** key with anyone outside the network, as it can be used to decrypt the central config. Leaking this public key will compromise the confidentiality of your network topology, but it will not compromise the security of the network itself.

If you run `nylon seal` on the central config, it will encrypt and sign the config using this public key. You may then distribute the sealed config to any http server, S3 bucket, etc (which supports GET), and put the URL in the node config under `dist/url`. Nylon will automatically download and verify the config before starting up. The config is versioned with a timestamp.

#### Central Config

You can modify the `sample-central.yaml` to define your network topology. Here are a few things you should configure **for each node**:
- `id`: A unique identifier for the node, this will also be used as a service id for the node.
- `pubkey`: The public key of the node, used to identify the node in the network, and to encrypt traffic to the node.
- `endpoints`: A list of endpoints that the node can be reached at (whether over LAN or internet). Each endpoint should be in the format of `host:port`. You can specify multiple endpoints for a node, and Nylon will automatically pick the best one to use.
- `address`: The primary IP address of the node in the Nylon network.
- `services`: An additional list of services that the node provides. This is used to define other addresses associated with the node, and to allow anycast routing. Multiple nodes can provide the same service, and Nylon will automatically route to the nearest one. Each service should have a unique `id`, and an `address`, which is declared at the end of the file.

> [!NOTE]
> **Difference between routers and clients:**
> - A router is an active nylon node that participates in routing, and can forward packets to other nodes. Routers should have at least one endpoint that is reachable by other nodes.
> - A client is a vanilla WireGuard client that does not participate in routing, and must connect through a router. Clients should not have any endpoints.

You must declare a graph, which defines your network topology. Each edge in the graph represents a bidirectional link between two nodes. You may simply connect every node to every other node, but this is always not necessary.

Now, on every node, copy the same `central.yaml` file.

You can now sync this file across all the nodes using `scp` or any other method you prefer.

Notice nodes can have 0 or more accessible endpoints. Nylon will regularly try to reach out to neighbours with published endpoints, and pick the most optimal endpoint (e.g we might have a public ip and a LAN ip).

### Running the network

Before running Nylon, make sure to open UDP port `57175` (or whatever port you configured) so that Nylon can communicate. Without further to do, simply run `./nylon run` (you may need CAP_NET_ADMIN or sudo).

After a while (5-10 seconds), you will notice that the network has converged!

> [!TIP]
> You can check the status of the node by running `./nylon i <interface_name>`. This will show you the current routing table, peers, and other useful information.