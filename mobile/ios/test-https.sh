#!/bin/sh
set -eu
cd "$(dirname "$0")"
output=$(mktemp -d "${TMPDIR:-/tmp}/meldnet-ios-https.XXXXXX")
server_pid=
cleanup() {
  if [ -n "$server_pid" ]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  rm -rf "$output"
}
trap cleanup EXIT
cp Tests/https-server.go.txt "$output/server.go"
go build -o "$output/server" "$output/server.go"
"$output/server" "$output/servers.json" &
server_pid=$!
count=0
while [ ! -f "$output/servers.json" ]; do
  count=$((count + 1))
  if [ "$count" -gt 50 ]; then echo 'Loopback fixture failed to start' >&2; exit 1; fi
  sleep 0.1
done
# Build the same C ABI shim and shared Go transport used inside the extension.
mkdir -p "$output/meldnetcontrol" "$output/module"
cp ../native/control/control.go "$output/meldnetcontrol/control.go"
cp Native/meldnet-control.go.txt "$output/bridge.go"
printf 'module golang.zx2c4.com/wireguard/apple\n\ngo 1.27.0\n' > "$output/go.mod"
printf 'package main\nfunc main() {}\n' > "$output/main.go"
(cd "$output" && go build -buildmode=c-archive -o libcontrol.a bridge.go main.go)
cp Native/meldnet-control.h "$output/module/meldnet-control.h"
printf 'module WireGuardKitGo { header "meldnet-control.h" export * }\n' > "$output/module/module.modulemap"
xcrun swiftc Shared/Store.swift Shared/NetworkValidation.swift PacketTunnel/GoTransport.swift Tests/HTTPS.swift -I "$output/module" -L "$output" -lcontrol -o "$output/https-tests"
"$output/https-tests" "$output/servers.json"
