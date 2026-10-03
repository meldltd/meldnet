#!/usr/bin/env bash
# Runs only inside disposable containers. Never uses --network=host or privileged.
set -euo pipefail
cd "$(dirname "$0")/.."
suffix="$$"
network="meldnet-test-$suffix"
left="meldnet-left-$suffix"
right="meldnet-right-$suffix"
cleanup() {
  result=$?
  if [ "$result" -ne 0 ]; then
    docker logs "$left" 2>/dev/null || true
    docker logs "$right" 2>/dev/null || true
  fi
  docker rm -f "$left" "$right" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  exit "$result"
}
trap cleanup EXIT
arch="$(docker info --format '{{.Architecture}}')"
case "$arch" in aarch64|arm64) arch=arm64;; x86_64|amd64) arch=amd64;; *) echo "Unsupported Docker architecture: $arch"; exit 1;; esac
docker build --build-arg "TARGETARCH=$arch" -f scripts/integration.Dockerfile -t meldnet-integration:local .
docker network create --internal "$network" >/dev/null
for node in "$left" "$right"; do
  # A separate PID 1 retains the namespace when testing a daemon-only crash.
  client_uid=0
  if [ "$node" = "$right" ]; then client_uid=65534; fi
  docker run -d --name "$node" --network "$network" --cap-add NET_ADMIN --device /dev/net/tun --entrypoint sh meldnet-integration:local -c 'umask 077; PATH=/no-external-tools /usr/local/bin/meldnetd --allow-uid "$1" & echo $! > /run/meldnet.pid; exec sleep infinity' sh "$client_uid" >/dev/null
done
wait_ready() {
  ready=false
  for attempt in $(seq 1 30); do
    if docker exec "$1" meldnet status >/dev/null 2>&1; then ready=true; break; fi
    sleep 0.2
  done
  if [ "$ready" != true ]; then echo "Daemon did not start: $1"; exit 1; fi
}
for node in "$left" "$right"; do
  wait_ready "$node"
done
docker exec "$left" meldnet init --name left --addresses 10.77.0.1/24,fd77::1/64 >/dev/null
docker exec "$right" meldnet init --name right --addresses 10.77.0.2/24,fd77::2/64 >/dev/null
docker exec --user 65534 "$right" meldnet status >/dev/null
docker exec --user 65534 "$right" sh -c 'test ! -r /var/lib/meldnet/node.json'
if docker exec --user 65533 "$right" meldnet status >/dev/null 2>&1; then
  echo "Unauthorized UID reached control socket"; exit 1
fi
# The socket owner can chmod it; kernel peer credentials must still reject others.
docker exec --user 65534 "$right" chmod 0666 /var/run/meldnet/control.sock
if docker exec --user 65533 "$right" meldnet status >/dev/null 2>&1; then
  echo "Peer-credential authentication failed"; exit 1
fi
docker exec --user 65534 "$right" chmod 0600 /var/run/meldnet/control.sock
left_key="$(docker exec "$left" meldnet key)"
right_key="$(docker exec "$right" meldnet key)"
left_ip="$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$left")"
right_ip="$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$right")"
configure() {
  docker exec "$1" sh -c 'meldnet config | jq --arg name "$1" --arg key "$2" --arg endpoint "$3:51820" --arg ip "$4/32" --arg ip6 "$5/128" ".settings.peers = [{name: \$name, public_key: \$key, endpoint: \$endpoint, allowed_ips: [\$ip, \$ip6], keepalive: 1}]" | meldnet apply - >/dev/null' sh "$2" "$3" "$4" "$5" "$6"
}
configure "$left" right "$right_key" "$right_ip" 10.77.0.2 fd77::2
configure "$right" left "$left_key" "$left_ip" 10.77.0.1 fd77::1
docker exec "$left" meldnet up
docker exec "$right" meldnet up
docker exec "$left" test -e /sys/class/net/meldnet/tun_flags
docker exec "$right" test -e /sys/class/net/meldnet/tun_flags
docker exec "$left" ping -c 3 -W 3 10.77.0.2
docker exec "$right" ping -c 3 -W 3 10.77.0.1
docker exec "$left" ping6 -c 2 -W 3 fd77::2
docker exec "$right" ping6 -c 2 -W 3 fd77::1
for node in "$left" "$right"; do
  docker exec "$node" sh -c 'meldnet status | jq -e '\''.backend == "wireguard" and .tunnel.up and (.tunnel.peers[0].last_handshake != null) and (.tunnel.peers[0].received_bytes > 0)'\'' >/dev/null'
done
# A conflicting route must roll back TUN and endpoint changes, keeping the
# pre-existing underlay route and connectivity intact.
docker exec "$left" meldnet down
subnet="$(docker network inspect --format '{{(index .IPAM.Config 0).Subnet}}' "$network")"
docker exec "$left" sh -c 'meldnet config | jq --arg prefix "$1" '\''.settings.peers[0].allowed_ips = [$prefix]'\'' | meldnet apply - >/dev/null' sh "$subnet"
if docker exec "$left" meldnet up; then
  echo "Unexpectedly replaced a conflicting route"; exit 1
fi
docker exec "$left" sh -c 'test ! -d /sys/class/net/meldnet && test ! -e /var/lib/meldnet/runtime/routes.json'
docker exec "$left" ping -c 1 -W 3 "$right_ip"
# Exercise default routes and endpoint-loop prevention before crashing.
docker exec "$left" sh -c 'meldnet config | jq '\''.settings.peers[0].allowed_ips = ["0.0.0.0/0", "::/0"]'\'' | meldnet apply - >/dev/null'
docker exec "$left" meldnet up
docker exec "$left" ping -c 2 -W 3 10.77.0.2
docker exec "$left" ping6 -c 2 -W 3 fd77::2
docker exec "$left" test -e /var/lib/meldnet/runtime/routes.json
# An embedded daemon crash closes its TUN. Recovery removes endpoint routes.
before_key="$left_key"
docker exec "$left" sh -c 'kill -KILL "$(cat /run/meldnet.pid)"'
docker exec "$left" sh -c 'test ! -d /sys/class/net/meldnet'
docker exec -d "$left" sh -c 'echo $$ > /run/meldnet.pid; PATH=/no-external-tools exec /usr/local/bin/meldnetd'
wait_ready "$left"
test "$(docker exec "$left" meldnet key)" = "$before_key"
docker exec "$left" sh -c 'meldnet status | jq -e '\''.tunnel.up == false and (.error == null)'\'' >/dev/null'
docker exec "$left" test ! -e /var/lib/meldnet/runtime/routes.json
docker exec "$left" meldnet up
docker exec "$left" ping -c 2 -W 3 10.77.0.2
# Graceful daemon shutdown must tear down networking without any UI attached.
docker exec "$left" sh -c 'kill -TERM "$(cat /run/meldnet.pid)"'
for attempt in $(seq 1 50); do
  if docker exec "$left" sh -c 'test ! -d /sys/class/net/meldnet && test ! -e /var/lib/meldnet/runtime/routes.json'; then break; fi
  sleep 0.2
done
docker exec "$left" sh -c 'test ! -d /sys/class/net/meldnet'
docker exec -d "$left" sh -c 'echo $$ > /run/meldnet.pid; PATH=/no-external-tools exec /usr/local/bin/meldnetd'
wait_ready "$left"
test "$(docker exec "$left" meldnet key)" = "$before_key"
docker exec "$left" meldnet up
docker exec "$left" ping -c 2 -W 3 10.77.0.2
docker exec "$left" meldnet down
docker exec "$right" meldnet down
for node in "$left" "$right"; do
  docker exec "$node" sh -c 'meldnet status | jq -e '\''.tunnel.up == false'\'' >/dev/null'
  docker exec "$node" sh -c 'test ! -e /var/lib/meldnet/runtime/routes.json && test ! -d /sys/class/net/meldnet'
done
echo "PASS: embedded WireGuard with no tool PATH; IPv4/IPv6 traffic, default routes, endpoint protection, UID isolation, crash recovery, identity persistence, reconnect, teardown"
