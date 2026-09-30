import Foundation

// What the menu shows. Views read it; where the data comes from stays in here.
// ObservableObject, since @State is a macro the Command Line Tools can't expand (no SwiftUI macro plugin).
final class NodeStore: ObservableObject {
    let path: String
    @Published private(set) var nodes: [Node]? // nil when central.yaml can't be read

    init(path: String) {
        // absolute and with ~ expanded, so "Can't read" says where it looked
        self.path = URL(fileURLWithPath: NSString(string: path).expandingTildeInPath).path
        nodes = try? readNodes(String(contentsOfFile: self.path, encoding: .utf8))
    }
}
