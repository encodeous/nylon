import AppKit
import SwiftUI

@main
struct MenuApp: App {
    @StateObject private var store = NodeStore()

    init() {
        // menu bar only; without an app bundle it would otherwise start background-only
        NSApplication.shared.setActivationPolicy(.accessory)
    }

    var body: some Scene {
        MenuBarExtra("Nylon", systemImage: "network") {
            MenuContent(store: store)
        }
    }
}
