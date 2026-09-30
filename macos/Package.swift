// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Nylon",
    platforms: [.macOS(.v14)],
    dependencies: [.package(url: "https://github.com/jpsim/Yams.git", from: "6.2.2")],
    targets: [
        .executableTarget(name: "NylonApp", dependencies: ["Yams"]),
        .testTarget(name: "NylonAppTests", dependencies: ["NylonApp"]),
    ]
)
