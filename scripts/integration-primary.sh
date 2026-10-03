#!/usr/bin/env bash
# Isolated Linux test: clients have no shared underlay, only the primary joins both.
set -euo pipefail
trap 'echo "Integration failed at line $LINENO" >&2' ERR
cd "$(dirname "$0")/.."
suffix="$$"
primary="meldnet-primary-$suffix"
a="meldnet-a-$suffix"
b="meldnet-b-$suffix"
neta="meldnet-a-net-$suffix"
netb="meldnet-b-net-$suffix"
cleanup() {
 result=$?
 if [ "$result" -ne 0 ]; then
  for node in "$primary" "$a" "$b"; do docker logs "$node" 2>/dev/null || true; docker exec "$node" meldnet peers 2>/dev/null || true; docker exec "$node" meldnet status 2>/dev/null || true; done
 fi
 docker rm -f "$primary" "$a" "$b" >/dev/null 2>&1 || true
 docker network rm "$neta" "$netb" >/dev/null 2>&1 || true
 exit "$result"
}
trap cleanup EXIT
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
docker build --build-arg "TARGETARCH=$arch" -f scripts/integration.Dockerfile -t meldnet-integration:local . >/dev/null
docker network create --internal "$neta" >/dev/null
docker network create --internal "$netb" >/dev/null
# Keep namespaces and private state across daemon process crashes.
docker run -d --name "$primary" --sysctl net.ipv4.ip_forward=0 --network "$neta" --network-alias primary --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh meldnet-integration:local -c 'umask 077; PATH=/no-tools /usr/local/bin/meldnetd --primary --public-url https://primary:8443 --endpoint primary:51820 & echo $! >/run/meldnet.pid; exec sleep infinity' >/dev/null
docker network connect --alias primary "$netb" "$primary"
for pair in "$a:$neta" "$b:$netb"; do
 node="${pair%%:*}"; network="${pair#*:}"
 docker run -d --name "$node" --network "$network" --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh meldnet-integration:local -c 'umask 077; PATH=/no-tools /usr/local/bin/meldnetd & echo $! >/run/meldnet.pid; exec sleep infinity' >/dev/null
done
ready() { for attempt in $(seq 1 40); do if docker exec "$1" meldnet status >/dev/null 2>&1; then return; fi; sleep 0.2; done; return 1; }
for node in "$primary" "$a" "$b"; do ready "$node"; done
for pair in "$a:laptop" "$b:server"; do
 node="${pair%%:*}"; name="${pair#*:}"
 key="$(docker exec "$primary" meldnet invite)"
 printf '%s' "$key" | docker exec -i "$node" meldnet join --name "$name" --key-file -
done
unset key
# DNS readiness is independent from tunnel status. Fail visibly on resolver errors.
for node in "$primary" "$a" "$b"; do
 docker exec "$node" sh -c 'meldnet status | jq -e '\''.dns.active and .dns.domain == "meldnet.internal" and (.dns.error == null)'\'' >/dev/null'
done
ping_until() {
 for attempt in $(seq 1 45); do if docker exec "$1" ping -c 1 -W 1 "$2" >/dev/null 2>&1; then return; fi; sleep 0.2; done
 echo "VPN ping failed from $1 to $2";return 1
}
ping_until "$a" 10.77.0.3
ping_until "$b" 10.77.0.2
ping_until "$a" 10.77.0.1
# Host forwarding is disabled; only the embedded relay can carry this traffic.
docker exec "$primary" sh -c 'test "$(cat /proc/sys/net/ipv4/ip_forward)" = 0'
ping_until "$a" 10.77.0.3
for node in "$a" "$b"; do
 docker exec "$node" sh -c 'meldnet peers | jq -e '\''.role == "client" and (.members | length) == 3'\'' >/dev/null'
 docker exec "$node" sh -c 'meldnet status | jq -e '\''.backend == "wireguard" and .tunnel.up and (.tunnel.peers[0].last_handshake != null)'\'' >/dev/null'
done
# Query authoritative DNS over both transports, and ordinary OS name resolution.
test "$(docker exec "$a" dig +short +time=2 +tries=2 @10.77.0.1 server.meldnet.internal A)" = "10.77.0.3"
test "$(docker exec "$a" dig +tcp +short +time=2 +tries=2 @10.77.0.1 server.meldnet.internal A)" = "10.77.0.3"
test "$(docker exec "$a" dig +short +time=2 +tries=2 server.meldnet.internal A)" = "10.77.0.3"
test "$(docker exec "$a" dig +short -x 10.77.0.3)" = "server.meldnet.internal."
docker exec "$a" dig @10.77.0.1 nonexistent.meldnet.internal A | grep -q NXDOMAIN
docker exec "$a" dig @10.77.0.1 example.com A | grep -q REFUSED
# Docker's original DNS is still used for names outside the private zone.
test -n "$(docker exec "$a" dig +short primary A)"
docker exec "$a" ping -c 2 -W 3 server.meldnet.internal >/dev/null
docker exec "$primary" ping -c 1 -W 3 laptop.meldnet.internal >/dev/null
# Disconnect restores original resolver bytes; reconnect installs private DNS again.
docker exec "$a" meldnet down
docker exec "$a" sh -c '! grep -q "Managed by Meldnet" /etc/resolv.conf && test ! -e /var/lib/meldnet/dns-resolver.json'
docker exec "$a" meldnet up
ping_until "$a" 10.77.0.3
oldkey="$(docker exec "$a" meldnet key)"
for node in "$primary" "$a"; do
 docker exec "$node" sh -c 'kill -KILL "$(cat /run/meldnet.pid)"'
 docker exec -d "$node" sh -c 'echo $$ >/run/meldnet.pid; PATH=/no-tools exec /usr/local/bin/meldnetd'
 ready "$node"
done
ping_until "$a" 10.77.0.3
ping_until "$b" 10.77.0.2
test "$oldkey" = "$(docker exec "$a" meldnet key)"
test "$(docker exec "$a" dig +short server.meldnet.internal A)" = "10.77.0.3"
docker exec "$primary" meldnet revoke laptop
for attempt in $(seq 1 30); do
 if docker exec "$a" sh -c 'meldnet status | jq -e '\''.tunnel.up == false'\'' >/dev/null'; then break; fi
 sleep 0.5
done
docker exec "$a" sh -c 'meldnet status | jq -e '\''.tunnel.up == false'\'' >/dev/null'
if docker exec "$b" ping -c 1 -W 2 10.77.0.2 >/dev/null 2>&1; then echo 'Revoked client remains reachable';exit 1; fi
ping_until "$b" 10.77.0.1
docker exec "$b" dig @10.77.0.1 laptop.meldnet.internal A | grep -q NXDOMAIN
docker exec "$a" sh -c '! grep -q "Managed by Meldnet" /etc/resolv.conf && test ! -e /var/lib/meldnet/dns-resolver.json'
echo 'PASS: enrollment, private DNS UDP/TCP/PTR, OS resolution, upstream DNS preservation, resolver cleanup/recovery, encrypted relay, and revocation'
