#!/usr/bin/env bash
# Only a private D-Bus and loopback sockets; no host DNS or network administration.
set -euo pipefail
cd "$(dirname "$0")/.."
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -o bin/dns-linux.test ./internal/privatedns
docker build -t meldnet-dns-tests:local - <<'DOCKERFILE'
FROM alpine:3.21
RUN apk add --no-cache dbus
DOCKERFILE
docker run --rm --network none \
 -v "$PWD/bin/dns-linux.test:/dns.test:ro" \
 -e MELDNET_TEST_DBUS=1 -e DBUS_SYSTEM_BUS_ADDRESS=unix:path=/tmp/meldnet-test-bus \
 meldnet-dns-tests:local sh -c 'dbus-daemon --session --address=unix:path=/tmp/meldnet-test-bus --fork; exec /dns.test -test.v'
