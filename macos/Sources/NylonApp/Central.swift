import Foundation
import Yams

struct Node: Equatable {
    let name: String
    let address: String? // first mesh IP; nil for a node that only advertises prefixes
}

// central.yaml, just the parts shown here. Every router and client is a node.
private struct Central: Decodable {
    struct Entry: Decodable {
        let id: String
        let addresses: [String]?
    }
    let routers: [Entry]?
    let clients: [Entry]?
}

func readNodes(_ yaml: String) throws -> [Node] {
    let c = try YAMLDecoder().decode(Central.self, from: yaml)
    return ((c.routers ?? []) + (c.clients ?? [])).map { Node(name: $0.id, address: $0.addresses?.first) }
}
