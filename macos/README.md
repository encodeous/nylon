# Nylon for macOS

A menu bar app that lists the nodes in your nylon network, read from `central.yaml`. Clicking one copies its IP.

Build and run it. It needs only the Command Line Tools (`xcode-select --install`):

```bash
swift build -c release --package-path macos
macos/.build/release/NylonApp -central /path/to/central.yaml
```

Without `-central` it reads `central.yaml` in the current directory, like `nylon run`.

Tests (the `-plugin-path` flag is only needed without full Xcode):

```bash
swift test --package-path macos -Xswiftc -plugin-path -Xswiftc "$(xcode-select -p)/usr/lib/swift/host/plugins/testing"
```
