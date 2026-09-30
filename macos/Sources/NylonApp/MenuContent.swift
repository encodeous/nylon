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
        Button("Choose central.yaml…") { chooseCentral() }
        Button("Quit") { NSApplication.shared.terminate(nil) }
    }

    private func chooseCentral() {
        NSApp.activate() // a menu bar app runs in the background, so bring the picker to the front
        let panel = NSOpenPanel()
        if panel.runModal() == .OK, let url = panel.url {
            store.choose(url.path)
        }
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
