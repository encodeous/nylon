# Nylon for macOS

A menu bar app that lists the nodes in your nylon network with their IPs. Click a node to copy its IP.

## Run it

1. Install the Command Line Tools if you don't have them: `xcode-select --install`
2. From the repo root, build and start it:

   ```bash
   swift build -c release --package-path macos && macos/.build/release/NylonApp
   ```

3. Click the network icon in the menu bar, then "Choose central.yaml…" and pick your file. The app remembers it for next time.

To skip the picker, pass the file: `macos/.build/release/NylonApp -central /path/to/central.yaml`. With no file chosen, it reads `central.yaml` in the current directory, like `nylon run`.

## Test

```bash
swift test --package-path macos -Xswiftc -plugin-path -Xswiftc "$(xcode-select -p)/usr/lib/swift/host/plugins/testing"
```

The `-plugin-path` flag is only needed without full Xcode.
