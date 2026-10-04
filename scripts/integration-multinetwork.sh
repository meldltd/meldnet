#!/usr/bin/env bash
# Disposable Linux namespaces only: simultaneous encrypted networks and DNS.
set -euo pipefail
cd "$(dirname "$0")/.."
suffix="$$"
network="meldnet-multi-$suffix"
a="meldnet-multi-a-$suffix"
b="meldnet-multi-b-$suffix"
c="meldnet-multi-c-$suffix"
client="meldnet-multi-client-$suffix"
cleanup() {
 result=$?
 if [ "$result" -ne 0 ]; then for node in "$a" "$b" "$c" "$client"; do docker logs "$node" 2>/dev/null || true; docker exec "$node" cat /run/restart.log 2>/dev/null || true; done; fi
 docker rm -f "$a" "$b" "$c" "$client" >/dev/null 2>&1 || true
 docker network rm "$network" >/dev/null 2>&1 || true
 exit "$result"
}
trap cleanup EXIT
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) exit 1;; esac
docker build --build-arg "TARGETARCH=$arch" -f scripts/integration.Dockerfile -t meldnet-integration:local . >/dev/null
docker network create --internal "$network" >/dev/null
for spec in "$a:10.77.0.0/24" "$b:10.88.0.0/24" "$c:10.77.0.0/25"; do
 node="${spec%%:*}";pool="${spec#*:}"
 docker run -d --name "$node" --network "$network" --cap-add NET_ADMIN --device /dev/net/tun -e PATH=/no-tools meldnet-integration:local --primary --public-url "https://$node:8443" --endpoint "$node:51820" --pool "$pool" >/dev/null
done
docker run -d --name "$client" --network "$network" --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh meldnet-integration:local -c 'umask 077; PATH=/no-tools /usr/local/bin/meldnetd & echo $! >/run/meldnet.pid; exec sleep infinity' >/dev/null
ready() { for attempt in $(seq 1 50); do if docker exec "$1" /usr/local/bin/meldnet status >/dev/null 2>&1; then return; fi; sleep 0.2; done; return 1; }
for node in "$a" "$b" "$c" "$client"; do ready "$node"; done
join() { docker exec "$1" /usr/local/bin/meldnet invite | docker exec -i "$client" /usr/local/bin/meldnet --network "$2" join --name laptop --key-file - --auto-connect="$3" --connect="$4"; }
join "$a" work true true
join "$b" home false true
join "$c" overlap false false
if docker exec "$client" /usr/local/bin/meldnet --network overlap up; then echo 'Overlapping connection accepted';exit 1;fi
ping_until() { for attempt in $(seq 1 30); do if docker exec "$client" ping -c 1 -W 1 "$1" >/dev/null 2>&1; then return; fi;sleep 0.2;done;return 1; }
ping_until 10.77.0.1
ping_until 10.88.0.1
docker exec "$client" sh -c 'meldnet networks | jq -e '\''([.[] | select(.status.tunnel.up)] | length) == 2 and ([.[] | select(.id == "overlap")][0].warning | length) > 0'\'' >/dev/null'
test "$(docker exec "$client" dig +short primary.work.meldnet.internal)" = 10.77.0.1
test "$(docker exec "$client" dig +short primary.home.meldnet.internal)" = 10.88.0.1
docker exec "$client" dig primary.meldnet.internal | grep -q NXDOMAIN
docker exec "$client" meldnet --network work down
ping_until 10.88.0.1
docker exec "$client" dig primary.work.meldnet.internal | grep -q NXDOMAIN
docker exec "$client" meldnet --network overlap up
ping_until 10.77.0.1
docker exec "$client" meldnet --network overlap down
docker exec "$client" meldnet --network work up
oldkey="$(docker exec "$client" meldnet --network work key)"
docker exec "$client" sh -c 'pid="$(cat /run/meldnet.pid)"; kill -KILL "$pid"; for attempt in $(seq 1 50); do if [ ! -e "/proc/$pid/stat" ] || grep -q ") Z " "/proc/$pid/stat"; then exit 0; fi; sleep 0.1; done; exit 1'
docker exec -d "$client" sh -c 'echo $$ >/run/meldnet.pid; PATH=/no-tools exec /usr/local/bin/meldnetd >/run/restart.log 2>&1'
ready "$client"
ping_until 10.77.0.1
test "$oldkey" = "$(docker exec "$client" meldnet --network work key)"
docker exec "$client" sh -c 'meldnet networks | jq -e '\''([.[] | select(.status.tunnel.up)] | length) == 1 and ([.[] | select(.id == "home")][0].auto_connect == false)'\'' >/dev/null'
docker exec "$client" meldnet --network home up
ping_until 10.88.0.1
docker exec "$client" meldnet --network work down
docker exec "$client" meldnet --network home down
docker exec "$client" sh -c '! grep -q "Managed by Meldnet" /etc/resolv.conf && test ! -e /var/lib/meldnet/dns-resolver.json'
echo 'PASS: two simultaneous encrypted networks, overlap rejection, scoped DNS, independent disconnect, startup policy, crash recovery and identity preservation'
