# Architecture

```text
meldnet (Bubble Tea TUI / headless CLI) or Meldnet.app (macOS menu bar)
             |
     HTTP JSON v1 over Unix socket
             |
meldnetd -> profiles -> per-network service -> config store (private keys)
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
retains the journal, exposes an error, and is retryable via disconnect. Startup recovers routes and recreates tunnels for profiles marked auto-connect;
other profiles stay disconnected. It cannot adopt a dead userspace device.

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

## Private DNS

`internal/privatedns` is independent of presentation. The controller derives DNS
records from the enrolled registry and passes desired DNS settings to the service.
The registration/network response carries an optional `{domain,server}` DNS object;
older clients ignore it and newer clients leave DNS untouched for older primaries.
No existing enrollment or persisted primary options change during upgrade.

The primary binds authoritative UDP/TCP port 53 only on its VPN IP. Record snapshots
are atomic and updated on membership changes. Only enrolled source addresses and
the primary's own address may query it. The server provides A, PTR, SOA, and NS;
existing names queried for unsupported types return NODATA and unknown names return
NXDOMAIN. It refuses names outside the private zone and exact managed reverse pool.
Positive TTL is 30 seconds and negative SOA TTL five seconds. It has no recursion,
zone transfer, public listener, or external service dependency. Request concurrency,
TCP query count/timeouts, and UDP response sizes are bounded.

Hostnames normally lowercase the device name. Legacy invalid DNS labels and names
that collide ignoring case receive public-key-derived aliases. The public directory
is the source of truth for each final FQDN. No bare-name search domain is installed.

DNS lifecycle follows service lifecycle: tunnel creation precedes resolver setup;
resolver restoration precedes tunnel teardown. DNS activation failure is recorded
separately in `status.dns` and shown in the TUI, preserving otherwise-working VPN
connectivity. Cleanup errors propagate and the tunnel is still torn down. Paused
nodes stay paused; idempotent reconciliation preserves active DNS services. Empty
peer slices are normalized to avoid repeated reloads of a primary with no clients.

macOS uses forward and reverse `/etc/resolver` files without touching global DNS.
Linux uses resolved's D-Bus SetLinkDNS/SetLinkDomains/SetLinkDefaultRoute and RevertLink
when `/etc/resolv.conf` points applications at 127.0.0.53 and the service is present.
Only the Meldnet interface is configured; routing domains are private-only. DNSSEC
and DNS-over-TLS are disabled for this unsigned, WireGuard-protected private link.
Interface removal after a crash naturally removes resolved's link settings.

Other Linux systems use a loopback-only proxy at 127.77.0.1:53. The proxy routes
private queries exclusively to the primary and all others to original nameservers,
with TCP fallback for truncated UDP replies. It preserves existing upstream flags
and EDNS without duplicating OPT records. No query cache is added. Existing resolver
search/options are retained while nameserver lines temporarily point to the proxy.
After restoring the resolver file, the proxy keeps forwarding public queries for
six seconds to accommodate applications caching the old resolver address. Private
queries are disabled during this grace period; reconnect replaces the proxy.

File changes are journaled in owner-only `dns-resolver.json` (and `dns-reverse.json`
on macOS) before writing. Linux uses an in-place write to support bind-mounted
resolv.conf, comparing expected content first. Recovery handles partial owned writes,
restores originals/removes created files, and preserves external replacements.
Modified files still containing the ownership marker retain the journal and produce
an error. The daemon recovers before control-plane DNS resolution. A crashed Linux
fallback proxy leaves ordinary DNS unavailable until daemon restart/recovery;
service supervision is recommended. No host resolver changes occur in simulation.

## Next milestones

Direct connectivity/NAT traversal, seamless peer reconciliation, network ACLs,
certificate/credential rotation, IPv6 allocation, endpoint mobility, signed installers,
and mobile frontends remain future work. Windows native acceptance testing
and Linux desktop distribution testing remain required. Manual IPv4/IPv6 peers and
full-tunnel routes still work independently. No exit-node, NAT, or kill-switch
provisioning is included.

## macOS menu bar frontend

`macos/Meldnet` is a separate native Swift/AppKit executable, built without adding
presentation dependencies to any Go package. It reads `/v1/status` and
`/v1/network` over the authorized Unix socket on a background queue with bounded
socket I/O and response sizes. A three-second timer refreshes public directory
rows; NSPasteboard receives only the selected hostname. Failures clear the previous
snapshot. Simulation and stale directory data never imply VPN reachability.

A user LaunchAgent starts the menu app at login. A separate root LaunchDaemon
starts the VPN at boot, independent of GUI login. The development shell installer leaves a running foreground daemon untouched and
installs boot activation for the next restart; the packaged installer loads the
service immediately without requiring reboot.
Connection controls POST `/v1/up` and `/v1/down`; joining POSTs typed name/key
JSON to `/v1/network/join`. Enrollment uses an NSSecureTextField, never arguments,
logs or frontend persistence. A serial background queue keeps requests off the
main thread; pending operations disable duplicate actions, and both success and
failure trigger an observed-state refresh. API mutation waits are bounded at
40 seconds, accommodating the daemon's tunnel/DNS lifecycle. Non-200 responses
never echo raw server bodies or enrollment inputs. The daemon retains all
validation and key/tunnel ownership. App quit never sends a daemon mutation. Go releases remain CGO-disabled; the
optional macOS app uses Apple's built-in AppKit and Swift runtime.


The optional universal installer is built by `make macos-installer`. Its system
installation domain requests native administrator authorization; payload metadata
sets root ownership. The privileged executable is isolated under
`/Library/PrivilegedHelperTools`, while the menu app stays unprivileged. Installer
scripts authorize the current console UID on first install, preserve an existing
recognized service's UID on upgrade, and reject unsafe paths/custom configurations.
A UID-filtered system LaunchAgent avoids running a useful control UI for unrelated
accounts. Root-only logs are separate from private identity state. No reboot is
required: package scripts enable and bootstrap the daemon and the authorized
user's login agent when their domains/jobs are available. Already loaded jobs
are left alone. The user stops the previous foreground daemon; installer scripts
never kill, unload or restart it. Existing daemon ownership locks remain the
protection against competing live instances. Bootstrap failures propagate.
The default artifact is unsigned development packaging; optional Developer ID
signing is available, with notarization still a separate release step.


The menu's Open TUI action uses NSWorkspace to open a private `.command` document
in Terminal, without Apple Events or elevation. The document invokes the bundled
CLI with the app's selected socket and `tui` arguments, quoted literally, and
removes itself before execution. No daemon or key ownership moves into the UI.
Both app build paths bundle the CLI; their minimum macOS version is 13 to match
Go's deployment target. The daemon still launches no external tools.

## Multiple-network ownership

`internal/profiles` maintains an owner-only versioned `profiles.json` index. The
legacy root state remains the `default` profile; additional profiles live in
`profiles/<id>` with independent keys, control membership, settings and recovery
journals. The daemon holds one process-wide OS networking lock and creates a
separate embedded WireGuard engine/TUN per profile. Linux names are stable hashed
profile names; Darwin uses independent utun devices. Only `default` can host a
primary server; additional profiles currently enroll as clients.

A shared VPN coordinator atomically reserves interface networks, managed pools
and peer AllowedIPs before creating a tunnel. Equal and nested IPv4/IPv6 ranges
are rejected across profiles. Reservations survive failed cleanup; unresolved
startup recovery blocks other ranges conservatively. Background reconciliation,
legacy API requests and UI actions all use the same guard. Startup policy is
independent of current connection intent. Managed reconcilers can update membership
while paused without reconnecting. Saved auto-connect profiles start in sorted ID
order. Overlap failures stay paused; other startup failures may retry reconciliation.

One private-DNS hub owns host resolver edits and the loopback aggregate authority.
Each connected primary also serves its own VPN address. The aggregate publishes
`host.<local-profile>.meldnet.internal`; unqualified legacy aliases are exposed
only when unique among connected profiles. All configured private reverse pools
remain private while any managed network is connected, including disconnected
profiles, preventing misses from reaching public upstreams. macOS journals each
reverse-zone resolver file independently; Linux uses the journaled fallback proxy
for the aggregate. Final disconnect restores resolver state. No frontend reads
private state, invokes privileged commands or owns tunnel lifetime.

## Additional desktop platforms

Windows is an experimental supported build target for daemon/CLI/TUI, with OS
code in `_windows.go` files. Embedded wireguard-go uses Wintun's signed driver
DLL; networking still uses compiled Go and native Windows APIs with no child
process. IP Helper handles addresses/routes and journals LUID/name/next-hop plus
owned metric/protocol to preserve administrator replacements. The daemon runs
under LocalSystem's SCM lifecycle or explicitly elevated in the foreground.
Its protected named pipe serves the same HTTP JSON v1, with a single allowed
user SID and LocalSystem DACL, remote rejection, first-instance exclusivity and
client-side privileged-server-owner validation. Owner-only protected DACLs
replace Unix mode bits; reparse points are refused. State belongs to the VPN
owner, never to the frontend. Linux/macOS code and releases retain CGO_ENABLED=0,
and Windows Go builds do too; Windows additionally needs the bundled driver DLL.

Windows private DNS uses journaled local NRPT registry rules for only the private
forward/reverse namespaces and loopback aggregate address. Rules are new,
Meldnet-owned entries; existing foreign local or Group Policy NRPT settings
block activation. Recovery checks every existing value, preserves external
edits and retains failed-cleanup intent. Native DNS cache invalidation follows
registry publication/removal. These Windows APIs, policy notifications and SCM
installation require native VM acceptance testing before production claims;
cross-compilation and fake policy tests cannot establish working Windows traffic.
See `windows/README.md` for exact packaging and installation boundaries.

`linux/meldnet_gui.py` is a separate native GTK 3 frontend with an optional
Ayatana/AppIndicator tray, mirroring the macOS per-network menus and join controls.
A window is also available when a desktop lacks tray support. It uses only the
UID-authorized Unix API, validates server peer credentials, polls asynchronously,
refreshes after ambiguous mutation outcomes, masks/clears enrollment and copies
only public hostnames. Neither frontend exit nor window close changes daemon
state. The GTK/Python presentation dependencies never enter Go daemon packages.
The desktop entry and optional systemd unit are packaged separately; installing
or activating a real service is an explicit administrator step.

Mobile has been assessed in `docs/mobile-platforms.md`. iOS needs a signed
Packet Tunnel Network Extension with WireGuardKit; Android needs an OS-controlled
VpnService with the upstream embedded backend. Neither is a packaged desktop
daemon or an implemented feature. Start with one active managed membership and
platform-owned DNS/secret persistence; preserve trusted-primary relay semantics.
