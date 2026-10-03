# Meldnet handoff

Updated: 2026-10-03

## Goal and current state

Go VPN daemon independent of its Bubble Tea TUI/CLI, supporting macOS and Linux.
Embedded wireguard-go and native OS networking require no separately installed
VPN tools. The latest milestone adds a Fiber primary registration service,
automatic client enrollment/IP configuration, a public device directory, and
client-to-client traffic through an embedded primary relay.

Implemented commands: `meldnetd --primary --public-url https://HOST:8443
--endpoint HOST:51820 [--listen :8443 --pool 10.77.0.0/24 --name primary]`,
`meldnet invite`, `meldnet join --name NAME --key-file FILE` (or stdin),
`meldnet peers`, and `meldnet revoke NAME`. Onboarding in the TUI uses `j` with
masked enrollment input. Managed settings are read-only; c/d controls the tunnel.
Manual peer setup remains available for independent use.

## Durable decisions

- Primary options, registry, invites, revocations, and client registration persist
  in owner-only atomic `network.json`. Existing manual nodes cannot silently become
  managed nodes. Use fresh state directories for initial primary/client setup.
- HTTPS uses Fiber v3.5.0, a generated persistent self-signed certificate, TLS 1.3,
  and exact certificate fingerprint pinning bundled into enrollment keys. Keys
  expire after one hour and authorize one device; exact retries are idempotent.
- Client bearer credentials are generated/persisted before registration. The
  primary stores only SHA-256 hashes of API/enrollment credentials. WireGuard and
  TLS private keys never cross APIs, logs, arguments, or UI. Enrollment credentials
  are the explicit exception: returned once to the authorized local caller and
  pasted into the TUI or read from a private file/stdin.
- Each client keeps one WireGuard peer (primary), routes the managed pool to it,
  and uses a 25-second keepalive. Primary peers each allow only the assigned /32.
  The primary learns roaming client endpoints from authenticated packets.
- The primary is a trusted hub: it decrypts/re-encrypts transit traffic. This is
  not direct mesh or end-to-end encryption between clients. The relay wrapper
  accepts only enrolled sources and primary/member destinations, decrements IPv4
  TTL, recomputes checksums, and bounds its packet queue. OS forwarding/NAT is not
  required. The primary must remain reachable over HTTPS and WireGuard UDP.
- Allocation is serialized and saved before response. Private IPv4 /16–/28 pools,
  primary at first usable IP, clients thereafter, max 128 lifetime registrations.
  Revoked IPs/names are deliberately not reused; smaller pools exhaust sooner.
- Controllers reconcile every three seconds. Managed daemons reconnect after
  restart; manual nodes retain explicit-connect semantics. Down pauses until up
  or restart. UI exit never stops the daemon. Revocation removes the primary
  peer and the client stops on its next authenticated HTTP denial.
- Membership changes currently recreate the primary tunnel. Existing clients may
  briefly lose traffic while WireGuard handshakes recover. Unchanged settings do
  not recreate it. Control outages preserve established tunnels and surface errors.
- wireguard-go device/conn/tun are embedded, logging disabled, no UAPI exposed.
  Native Linux Netlink and Darwin ioctl/PF_ROUTE configure non-persistent TUNs.
  Preserve endpoint-route journals until cleanup succeeds; do not replace existing
  administrator routes. Only AllowedIPs create routes; defaults become /1 pairs.
- Fiber imports os/exec transitively for optional prefork. Prefork is disabled.
  Make checks prohibit direct application os/exec and daemon presentation imports.
  Runtime containers test with an empty executable PATH. CGO-disabled builds remain.
- Local socket peer credentials and ownership protect the privileged control API.
  One production networking owner per filesystem via /var/run/meldnet-network.lock.
- Simulation never sends VPN traffic or fabricates handshakes. Managed simulation
  does send real HTTPS registration/synchronization traffic.

## Code map

- `cmd/meldnetd`: daemon flags, local API, managed controller/Fiber lifecycle.
- `cmd/meldnet`: TUI and headless commands; enrollment from file/stdin only.
- `internal/control`: enrollment, pinned TLS, Fiber API, allocation/revocation,
  private registry, autonomous client, state validation.
- `internal/service/managed.go`: managed identity and serialized reconciliation.
- `internal/service/service.go`: manual revisions/lifecycle, managed guard/pause.
- `internal/vpn/relay.go`: primary in-process IPv4 relay over a TUN wrapper.
- `internal/vpn/wireguard.go`: embedded engine, endpoint planning, journal recovery.
- `internal/vpn/network*.go`: native addresses/routes; `status.go`: public allowlist.
- `internal/config`, `internal/securefs`: identity, atomic owner-only storage, locks.
- `internal/api`: protected local HTTP JSON API shared by frontends.
- `internal/tui`: masked join form, managed directory, manual settings/status.
- `scripts/integration-primary.sh`: primary plus two isolated client networks,
  real traffic, no OS forwarding, crash/restart, and revocation.
- `scripts/integration-linux.sh`: manual IPv4/IPv6, route/UID/recovery regression.
- `scripts/smoke-managed-tui.py`, `scripts/smoke-tui.py`: real PTY interactions
  against temporary unprivileged simulated daemons.
- `packaging`: optional systemd/launchd templates, not installed on host.

## Verification

- `make check build cross`: passed on macOS arm64. Formatting, vet, dependency
  guards, race-enabled unit/API/TUI tests, and CGO-disabled builds for
  darwin/linux × amd64/arm64.
- Control tests cover pinned HTTPS, anonymous denial, expired/reused invites,
  concurrent unique allocation, idempotent retries, durable identity/registry,
  replacement keys after failed enrollment, recovery after a lost registration
  response and expired invite, credential hash storage, revocation,
  automatic connection, and rejecting manual edits on managed nodes.
- Relay tests cover destination/source restrictions, primary delivery, TTL,
  checksum repair, buffer independence, malformed headers, and utun read headroom.
- `scripts/integration-primary.sh`: passed with real embedded WireGuard on Linux
  arm64. Two clients on separate Docker networks reached each other and primary
  with primary OS IPv4 forwarding disabled. Automatic addresses, discovery,
  handshakes, SIGKILL/restart of primary and client, identity persistence, revoked
  client teardown/unreachability, and surviving client connectivity passed.
- `scripts/integration-linux.sh`: passed; real manual IPv4/IPv6 traffic, full
  routes, endpoint bypass, route-conflict rollback, UID/private-file isolation,
  crash recovery, identity preservation, reconnect, and teardown remain working.
- `python3 scripts/smoke-managed-tui.py`: passed in an 80×24 PTY; masked key paste,
  automatic assignment/connection, directory rendering, and UI quit independence.
- `python3 scripts/smoke-tui.py`: passed; manual TUI regression and quit independence.
- Baseline Darwin ioctl/route encoding/read-only route lookup,
  and engine rollback/journal tests remain part of the project; check runs the
  applicable Go tests. Native macOS encrypted traffic has NOT been verified.

Tests create no host VPN or installed service. Docker tests grant NET_ADMIN/TUN
only to disposable containers on internal networks; all are cleaned up on exit.
The integration image is cached. Repository is on main with uncommitted files;
no remote, commit, or pull request was created.

## Limitations and next actions

1. Validate privileged macOS utun setup and encrypted traffic with actual peers.
2. Replace membership-triggered primary tunnel recreation with live peer/route
   reconciliation to avoid interruption to existing sessions.
3. Add certificate/credential rotation and device re-enrollment/address reuse.
   Current primary TLS certificate lasts five years; back up complete state.
4. Add managed IPv6, direct NAT traversal/mesh, DNS, ACLs, endpoint mobility, and
   release/service-install testing if continuing toward broader Tailscale parity.

No DNS, exit nodes, NAT/firewall provisioning, kill switch, direct mesh, or automatic
underlay/endpoint refresh. Primary availability/bandwidth bounds the whole network.
No automatic migration of existing manual identities into managed mode. Manual
IPv4/IPv6 features are independent from the IPv4-only managed pool.

## Handoff method

Read this file and docs/architecture.md before working. Update current state,
exact verification, limitations, and ordered next steps before handing back.
Preserve durable decisions, remove obsolete progress notes, and never record secrets.
