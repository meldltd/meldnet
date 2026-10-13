#!/usr/bin/env bash
# Actual WireGuard TCP throughput, isolated containers only. No host VPN changes.
set -euo pipefail
cd "$(dirname "$0")/.."
suffix="$$"
network="meldnet-bench-$suffix"
primary="meldnet-bench-primary-$suffix"
a="meldnet-bench-a-$suffix"
b="meldnet-bench-b-$suffix"
seconds="${BENCH_SECONDS:-10}"
output="${BENCH_OUTPUT:-bin/throughput}"
case "$seconds" in ''|0|*[!0-9]*) echo 'BENCH_SECONDS must be a positive integer' >&2; exit 1;; esac
mkdir -p "$output"
cleanup() {
 result=$?
 docker rm -f "$primary" "$a" "$b" >/dev/null 2>&1 || true
 docker network rm "$network" >/dev/null 2>&1 || true
 exit "$result"
}
trap cleanup EXIT
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "bin/linux-$arch/meldnet" ./cmd/meldnet
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "bin/linux-$arch/meldnetd" ./cmd/meldnetd
docker build --build-arg "TARGETARCH=$arch" -f scripts/integration.Dockerfile -t meldnet-integration:local . >/dev/null
docker build -t meldnet-throughput:local - <<'DOCKER' >/dev/null
FROM meldnet-integration:local
RUN apk add --no-cache iperf3
DOCKER
docker network create --internal "$network" >/dev/null
docker run -d --name "$primary" --network "$network" --network-alias primary --sysctl net.ipv4.ip_forward=0 --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh meldnet-throughput:local -c 'PATH=/no-tools /usr/local/bin/meldnetd --primary --public-url https://primary:8443 --endpoint primary:51820 & exec sleep infinity' >/dev/null
for node in "$a" "$b"; do
 docker run -d --name "$node" --network "$network" --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh meldnet-throughput:local -c 'PATH=/no-tools /usr/local/bin/meldnetd & exec sleep infinity' >/dev/null
done
ready() { for attempt in $(seq 1 60); do if docker exec "$1" meldnet status >/dev/null 2>&1; then return; fi; sleep 0.2; done; return 1; }
for node in "$primary" "$a" "$b"; do ready "$node"; done
for pair in "$a:sender" "$b:receiver"; do
 node="${pair%%:*}"; name="${pair#*:}"
 # Secret goes straight between authorized local APIs, never into arguments/logs.
 docker exec "$primary" meldnet invite | docker exec -i "$node" meldnet join --name "$name" --key-file - >/dev/null
done
for attempt in $(seq 1 60); do
 if docker exec "$a" ping -c 1 -W 1 10.77.0.3 >/dev/null 2>&1; then break; fi
 sleep 0.2
done
docker exec "$a" ping -c 1 -W 2 10.77.0.3 >/dev/null
docker exec -d "$b" iperf3 -s
docker exec -d "$primary" iperf3 -s
underlay="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$b")"
# The initial second is omitted to reduce handshake/slow-start noise.
for pair in "direct:$underlay" 'primary:10.77.0.1' 'relay:10.77.0.3'; do
 label="${pair%%:*}"; address="${pair#*:}"
 docker exec "$a" iperf3 -c "$address" -t "$seconds" -O 1 -J >"$output/$label.json"
 jq -e 'has("error") | not' "$output/$label.json" >/dev/null
 jq -r --arg label "$label" '[ $label, (.end.sum_received.bits_per_second / 1000000 | tostring), "Mbit/s", (.end.sum_sent.retransmits | tostring), "retransmits" ] | join(" ")' "$output/$label.json"
done
# Reverse direction catches asymmetric relay/host paths.
docker exec "$a" iperf3 -c 10.77.0.3 -t "$seconds" -O 1 -R -J >"$output/relay-reverse.json"
jq -e 'has("error") | not' "$output/relay-reverse.json" >/dev/null
jq -r '["relay-reverse", (.end.sum_received.bits_per_second / 1000000 | tostring), "Mbit/s"] | join(" ")' "$output/relay-reverse.json"
echo "Results: $output (real encrypted Linux container traffic; not native macOS/Windows or WAN performance)"
