import Testing
@testable import NylonApp

// Block and flow lists, prefix objects the app ignores, and a router with no addresses.
@Test func readsNodes() throws {
    let yaml = """
    routers:
      - id: alice
        pubkey: k
        endpoints: ["alice.example.com:57175"]
        addresses:
          - 10.0.0.1
          - fd00::1
        prefixes:
          - type: static
            prefix: 192.168.0.0/24
      - id: relay
        pubkey: k
    clients:
      - id: phone
        pubkey: k
        addresses: [10.0.0.20]
    graph:
      - alice, relay, phone
    """
    #expect(try readNodes(yaml) == [
        Node(name: "alice", address: "10.0.0.1"),
        Node(name: "relay", address: nil),
        Node(name: "phone", address: "10.0.0.20"),
    ])
}

@Test func rejectsMalformedYAML() {
    #expect(throws: (any Error).self) { try readNodes("routers: [") }
}
