#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(uname -s) == Darwin ]] || { echo 'Build the menu bar app on macOS.' >&2; exit 1; }
app='bin/Meldnet.app'
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
swiftc -swift-version 5 -O -framework AppKit -target "$(uname -m)-apple-macosx13.0" macos/Meldnet/API.swift macos/Meldnet/Icon.swift macos/Meldnet/TUILauncher.swift macos/Meldnet/main.swift -o "$app/Contents/MacOS/Meldnet"
cp macos/Info.plist "$app/Contents/Info.plist"
CGO_ENABLED=0 go build -trimpath -o "$app/Contents/Resources/meldnet" ./cmd/meldnet
codesign --force --sign - "$app/Contents/Resources/meldnet"
codesign --force --sign - "$app"
plutil -lint "$app/Contents/Info.plist"
