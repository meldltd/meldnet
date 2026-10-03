# Architecture

```text
meldnet (Bubble Tea TUI / headless CLI)
             |
     HTTP JSON v1 over Unix socket
             |
meldnetd -> service -> config store (private keys)
                |
             vpn.Engine
                |
   embedded wireguard-go / explicit simulation
                |
     TUN + native addresses and routing
```

The daemon runs independently. Neither frontend shutdown nor IPC disconnection
changes tunnel state. The service serializes mutations and checks observed tunnel
state before allowing configuration changes. Settings are immutable while up;
disconnect, edit, and reconnect. Revisions reject stale configuration writes.

The socket is local only, mode 0600, owned by the allowed user, inside a directory
owned by the daemon. Kernel peer credentials enforce the selected UID (and root)
even if the socket owner widens its file permissions. The real daemon runs as root. The authorized user may change
VPN routes through the API but may not execute arbitrary commands. Configuration
is typed and applied directly to the library: hooks, arbitrary file paths, and
shell directives cannot be submitted. Private keys never cross IPC. The API rejects
browser-origin requests and unexpected Host headers as additional hardening.

Persistence uses owner-only directories and atomic files. State/socket locks
prevent competing instances; a production networking lock also prevents separate
state directories from competing for host routes. WireGuard is compiled into the
daemon and owns a non-persistent TUN and UDP sockets. There is no child process,
external configuration file, or WireGuard UAPI socket. Linux uses Netlink; macOS
uses native ioctls and PF_ROUTE sockets. No networking executable is used.

Connection order: validate/resolve endpoints, snapshot physical routes, create
TUN, configure WireGuard in memory, assign addresses, pin overlapping endpoint
paths, install AllowedIPs routes, and bring up the device. Partial failure closes
the device and rolls back endpoint routes. MTU is conservatively 1280. Defaults
become two `/1` routes; endpoint host routes prevent recursion. Full tunnels need
stable endpoints. Physical-network changes require reconnecting. Existing routes
are never replaced.

Daemon exit or crash closes TUN and removes attached routes. Physical endpoint
routes may outlive it, so intent is saved in private `runtime/routes.json` before
installation. Linux routes carry a dedicated protocol/priority, Darwin routes
carry protocol flags. Recovery deletes only exactly matching marked routes on
the recorded interface, leaving administrator replacements alone. Failed cleanup
retains the journal, exposes an error, and is retryable via disconnect. Startup recovers routes; managed nodes recreate the tunnel automatically, while
manual nodes stay disconnected. It cannot adopt a dead userspace device.

Status comes from the library's in-process configuration protocol. Its raw output
contains keys: a streaming allowlist retains only public peer keys, handshake
timestamps, and byte counters. Raw output is never logged, saved, or sent over the
API. Library logging is disabled. Interface health is checked separately; an
interface being up does not guarantee peer reachability.

## Managed network

`internal/control` sits above service/config/vpn. On the primary it serves Fiber v3
HTTPS separately from the protected Unix socket. On clients it polls that service.
Both frontends use only the Unix API; neither owns registration or tunnel state.
Fiber prefork and subprocess features are disabled. Its optional code transitively
imports os/exec, so the build guard prohibits direct application subprocess imports
and all daemon presentation dependencies instead of rejecting Fiber's dependency tree.

The primary stores `network.json` atomically in the private daemon directory:
options, allocated IPs, public keys, credential hashes, revocations, and expiring
invite hashes. It generates a persistent TLS identity in `primary-tls.pem`. The
versioned enrollment key bundles the HTTPS origin, SHA-256 certificate pin, and
256-bit secret. Certificates require the exact pin and valid dates, TLS 1.3, no
redirects, and no HTTP proxy. One-hour invites enroll exactly one identity;
retries with the same public key/name/credential hash are idempotent.

Clients persist their API credential before registering, keeping the enrollment
secret only until a successful response. The WireGuard identity is locally generated
and never uploaded. Interrupted enrollment resumes; restarting after acceptance
uses the device credential. Pending clients also try their persisted device
credential first, recovering a lost response even after the invite expires.
Unknown, consumed, expired, or revoked enrollment credentials fail.
The local API can issue invites and revoke devices only on a primary. Public status
contains directory entries and errors, never credentials or private keys.

The primary reserves the first usable IPv4 address; clients receive successive
/32 addresses. Allocation and registration are serialized and saved before response.
Private /16–/28 pools are supported with at most 128 lifetime client allocations;
revoked addresses and names are not reused. A client installs one peer, the primary,
with the entire pool as AllowedIPs and a 25-second keepalive. Each primary peer has
one client /32 and learns its UDP endpoint from authenticated inbound traffic.

`vpn/relay.go` wraps the primary's TUN. Authenticated packets to the primary's VPN
IP go to the OS; packets between enrolled clients return to WireGuard for encryption
to their destination. Unknown source/destination traffic is dropped. IPv4 TTL is
decremented and checksum repaired. Queues are bounded; congestion drops packets.
Host IP forwarding is unnecessary. The primary sees plaintext transit packets and
must be trusted; this topology is not an end-to-end client mesh.

Every three seconds the controller reconciles desired settings and refreshes the
directory. Membership changes currently close/recreate the primary device and routes,
so existing sessions can briefly lose traffic until handshake recovery. Unchanged
settings leave the device intact. Pausing via the local API survives reconciliation
until explicit connect or process restart. Managed modes reconnect at startup; manual
mode preserves its prior explicit-connect behavior. Revocation removes the primary
peer immediately and clients stop after receiving HTTP 401. Control unavailability
keeps existing tunnels running and appears as a synchronization error.

## Next milestones

Direct connectivity/NAT traversal, seamless peer reconciliation, DNS, network ACLs,
certificate/credential rotation, IPv6 allocation, endpoint mobility, signed installers,
and optional GUI/mobile frontends remain future work. Manual IPv4/IPv6 peers and
full-tunnel routes still work independently. No exit-node, NAT, DNS, or kill-switch
provisioning is included.
