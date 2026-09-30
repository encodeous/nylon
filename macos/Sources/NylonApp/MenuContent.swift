import AppKit
import SwiftUI

struct MenuContent: View {
    @ObservedObject var store: NodeStore

    var body: some View {
        if let nodes = store.nodes {
            ForEach(nodes, id: \.name) { NodeRow(node: $0) }
        } else {
            Text("Can't read \(store.path)")
        }
        Divider()
        Button("Quit") { NSApplication.shared.terminate(nil) }
    }
}

// A node's name and IP. Clicking it copies the IP.
struct NodeRow: View {
    let node: Node

    var body: some View {
        Button("\(node.name)  \(node.address ?? "no address")") {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(node.address ?? "", forType: .string)
        }
        .disabled(node.address == nil)
    }
}
