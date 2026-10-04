# Meldnet

A Go VPN service with a separate Bubble Tea terminal client for macOS and Linux,
and a native macOS menu bar app. The VPN lives in `meldnetd`; closing either
frontend does not disconnect it. Both use the same versioned local API.

**Current milestone:** a primary-managed WireGuard network with a Fiber HTTPS
registration backend, automatic IP allocation, private DNS, device discovery, and client-to-client
communication through the primary. WireGuard is embedded; manual peer configuration
and a clearly labeled simulation mode are also available.

There are **no separately installed VPN tools or runtime packages**. The Go
libraries are compiled into the binaries. Interface addresses and routing use
native OS calls; the daemon never launches networking commands.

## Build

Requires Go 1.27 or later. From this directory:

```sh
make build
make check
make cross
```

`bin/meldnet` is the TUI/headless client. `bin/meldnetd` is the daemon. Cross builds
are placed under `bin/{darwin,linux}-{amd64,arm64}/`. No C toolchain is required to
build the application binaries. Builds disable CGO; Linux binaries are static.
A terminal of at least 64 columns × 24 rows is
recommended.

## macOS menu bar and automatic startup

For the full VPN on macOS 13 or newer, with Apple's Command Line Tools installed:

```sh
./scripts/install-macos.sh
```

This installs `~/Applications/Meldnet.app`, starts its menu bar icon now, and
registers it to open at login. Click a peer row to copy its complete private
hostname. The list refreshes every three seconds and distinguishes recent VPN
handshakes from recent control-server contact. Quitting the menu app leaves the
VPN running. Each network has its own **Connect / Disconnect** and **Auto-connect
at startup** controls. **Join Network…** accepts a local network name, device name
and masked enrollment key, with separate connect-now and auto-connect choices.
Joining another network preserves existing memberships and connections. Conflicting
address ranges show a warning and cannot connect together.
**Open TUI** opens the bundled terminal client in Terminal as your current user,
using the same control socket.

The installer requests your administrator password to install `meldnetd` for the
**next boot**, preserving the currently running tunnel and existing default state.
Networks marked for auto-connect reconnect after boot; other networks stay disconnected. Custom state/socket paths need matching service/app arguments; do
not use this default installer for a daemon with custom paths.

For just the menu app, use `./scripts/install-macos.sh --menu-only`. To build/test
without installing anything, use `make macos-test`. Linux retains its TUI/CLI and
systemd service; the menu bar app is macOS-only. See [service setup and removal](docs/services.md).

## Build a macOS installer

```sh
make macos-installer
```

Produces `bin/Meldnet-0.1.0-universal.pkg` for Apple Silicon and Intel Macs
(macOS 13+). Use `make macos-installer VERSION=0.2.0` to set a release version.
Builds need Go and Apple's Command Line Tools; the resulting package needs no
separate runtime installation. Building does not require sudo or install anything.

Open the `.pkg` to install. macOS Installer requests administrator authorization,
installs `/Applications/Meldnet.app` and a root-owned daemon, and loads the
background service immediately. No reboot is required. Shut down any previous
foreground daemon yourself before installing. The menu app opens in the authorized
user's active desktop session, or on their next login. Boot/login startup remains enabled. Initial installation authorizes the active
console account; upgrades preserve the existing authorized UID. Existing default
VPN identity and enrollment data are preserved.

The default package is for local development: code is ad-hoc signed and the
installer is unsigned, not notarized. Developer ID signing is supported with
`CODE_SIGN_IDENTITY` and `INSTALLER_SIGN_IDENTITY`; see [installer details](docs/services.md#macos-installer-package).
Use `make macos-installer-test` to build and check the archive and permission
scripts in a temporary filesystem without installing a service or changing host DNS.

## Multiple networks

Join each network under a local name (1–31 lowercase letters, digits or hyphens;
start with a letter and end with a letter or digit). Keep enrollment keys in
private files or provide them on stdin, never in arguments.

```sh
./bin/meldnet --network work join --name laptop --key-file /path/to/work-key
./bin/meldnet --network home join --name laptop --key-file /path/to/home-key --auto-connect=false --connect=false
./bin/meldnet networks
./bin/meldnet --network home up
./bin/meldnet --network work down
./bin/meldnet --network home autoconnect on
```

Up to 16 profiles can be saved, including `default`. Each has separate keys,
configuration, connection state and startup preference. Changing auto-connect
does not change the current connection. Disconnect pauses that profile until
explicitly connected again, or until a daemon restart if auto-connect is enabled.
Existing enrollment becomes `default` without replacing its identity; its previous
auto-start behavior is preserved. Commands without `--network` use `default`.

The daemon rejects simultaneous equal **or overlapping** ranges, including
subnets and IPv6 routes. The warning identifies the conflicting network and ranges.
You can save conflicting memberships and switch between them. If a join succeeds
but connecting conflicts, the membership remains saved. At startup, profiles are
attempted in name order; a conflicting profile stays disconnected. Failed route
cleanup also prevents conflicting connections until cleanup succeeds.

In the TUI press **n** for networks; select a row, then **c** to connect, **d** to
disconnect, **a** to toggle auto-connect, **Enter** for peers, or **+** to join another.
The macOS menu provides the same choices under each network.

Private hostnames include the local network name, for example
`server.work.meldnet.internal`. Click a peer in the menu to copy its hostname.
The old `server.meldnet.internal` alias works only when that name is unique among
connected networks; ambiguous names return NXDOMAIN. These profile names are local
to this computer. Private DNS uses a shared local listener at `127.0.0.1:53`;
if another service owns it, the network reports a DNS error. Disconnecting one
network removes only its records and preserves the others.

## Try the TUI without modifying networking

In one terminal:

```sh
make demo
```

In a second terminal, from this project directory:

```sh
./bin/meldnet --socket "$PWD/.demo/control.sock"
```

Press **Enter** to initialize your device. Choose a name and an interface address,
then press **a** to add a peer. Simulation creates keys and saves configuration but
never creates a network interface, sends VPN packets, or invents handshakes.
Managed-mode simulation still sends real HTTPS registration/synchronization requests.

Demo data persists in `.demo/state/`. It is separate from real VPN state. Stop the
demo daemon with Ctrl+C when finished.

## Run a real VPN

Run the two binaries directly. Linux requires the operating system's TUN device
(`/dev/net/tun`), not its WireGuard kernel module. macOS uses its built-in utun
facility. Root/network administration privileges are still required. No Homebrew
or distribution VPN package is needed. Containers must expose `/dev/net/tun`
and grant NET_ADMIN.

Start the daemon in a terminal, granting your normal user control of its socket:

```sh
sudo ./bin/meldnetd --allow-uid "$(id -u)"
```

In another terminal, **as your normal user**:

```sh
./bin/meldnet
```

The daemon must stay running. Root owns the state directory; your user gets
access only to the control socket. This UID can configure VPN routes and peers.
It does not receive the private key. Do not run the TUI with sudo.

Default paths:

| Item | macOS | Linux |
|---|---|---|
| Private state | `/Library/Application Support/Meldnet` | `/var/lib/meldnet` |
| Control socket | `/var/run/meldnet/control.sock` | `/var/run/meldnet/control.sock` |

The same `--socket` override must be provided to both processes. State and socket
directories must be owned by the daemon user. Use a dedicated socket directory;
the daemon will not bind directly in a shared writable directory such as `/tmp`.
Only one production daemon per filesystem instance is supported, enforced by
`/var/run/meldnet-network.lock`. It owns one tunnel (`meldnet` on Linux,
dynamically assigned `utun` on macOS). Keep using the same state directory after
a crash so startup can recover its route journal.

### Automatic setup with a primary

Use a fresh state directory for each managed device. Choose a private pool that
avoids your existing LAN/VPN ranges. The primary must be reachable by every client
on HTTPS TCP 8443 and WireGuard UDP 51820 (forward these ports if it is behind NAT).
Clients need no inbound port forwarding. Substitute your actual hostname:

```sh
sudo ./bin/meldnetd --allow-uid "$(id -u)" --primary \
  --public-url https://vpn.example.com:8443 \
  --endpoint vpn.example.com:51820 --pool 10.77.0.0/24
```

From another terminal on the primary, generate one enrollment key per device:

```sh
./bin/meldnet invite
```

The key expires after one hour and enrolls one device. Transfer it privately to
that device. It contains the primary URL, a TLS certificate fingerprint, and an
enrollment secret. No separate certificate installation is needed.

On each client, start `sudo ./bin/meldnetd --allow-uid "$(id -u)"`, then open
`./bin/meldnet` and press **j**. Enter a unique device name and paste the key into
the masked field. The daemon generates its WireGuard private key locally, receives
an address, configures routes, and connects. Alternatively, read the key from a
private file, avoiding shell-history exposure:

```sh
./bin/meldnet join --name laptop --key-file /path/to/private-enrollment-key
./bin/meldnet peers
```

`--key-file -` reads stdin. The primary receives `.1`, and clients get stable
addresses starting at `.2`. `meldnet peers` and the TUI list names and VPN IPs;
use their private hostnames or IPs to reach other devices. Activity timestamps report control contact,
not a successful WireGuard handshake. Host firewalls still govern local services.

On the primary, `meldnet revoke laptop` removes the device's WireGuard peer and
API authorization. Clients synchronize every three seconds; a revoked client
stops its tunnel when it receives the denial. Managed configuration cannot be
edited manually. **c/d** or `up/down` reconnect/pause until daemon restart.

Restart any managed daemon with its normal command and the same state directory;
primary options and client registration persist. Managed devices reconnect
automatically. The UI need not be running.

Traffic uses an embedded primary relay: the primary decrypts and re-encrypts
client-to-client packets and must be trusted with their contents. This is a hub
network, with no direct mesh links or end-to-end encryption between clients beyond
application protocols. No OS IP-forwarding switch, NAT rules, or relay executable
is needed. Keep the primary online; its availability and bandwidth serve the network.

Current managed-mode limits: IPv4 private pools /16 through /28, up to 128 lifetime
client registrations (smaller pools have fewer addresses), unique device names,
no address/name reuse after revocation, and a brief primary tunnel reload when
membership changes. Existing clients can take a WireGuard handshake retry to
recover after a reload. ACL groups, exit nodes, direct NAT traversal,
and automatic endpoint refresh are not implemented. The self-signed primary TLS
identity is valid for five years; certificate rotation needs an explicit future
migration path. Back up the owner-only state directory securely.

### Private DNS

Managed nodes automatically get fully qualified names under **meldnet.internal**:

```sh
ping primary.meldnet.internal
ping imac.meldnet.internal
./bin/meldnet peers    # includes each device's hostname
./bin/meldnet status   # includes DNS mode, active state, and any resolver error
```

Upgrade and restart both the primary and clients, keeping their existing state
folders. No new enrollment keys or re-registration are needed. Upgrade the primary
first; a new client talking to an older primary leaves DNS unchanged until the
primary advertises DNS support.

The primary serves authoritative A and PTR records on its VPN address, UDP and TCP
port 53. It answers registered source addresses only and does not provide public
DNS recursion. Records reflect the registry, including currently offline devices;
revocation removes their records. Positive TTL is 30 seconds, negative TTL five
seconds. Names are case-insensitive. Ordinary device names become
`name.meldnet.internal`; legacy names containing dots/underscores or colliding
ignoring case receive a deterministic `node-…` hostname shown in `meldnet peers`.
Use the complete hostname; automatic short-name search suffixes are not installed.

No public DNS record, certificate, extra DNS package, or Nginx configuration is
needed. DNS travels inside WireGuard. A firewall on the primary must permit UDP
and TCP port 53 **on the VPN interface**; do not publish DNS port 53 externally.
A service already binding port 53 on all primary addresses may conflict; this
appears in `status.dns.error` while the VPN remains usable by IP.

Host integration:

- **macOS:** creates domain-specific files in `/etc/resolver` for the private
  domain and pool's reverse zone. Other DNS configuration is untouched. Use
  `ping`, normal applications, or `dscacheutil -q host -a name imac.meldnet.internal`
  to test system resolution. macOS `dig` bypasses split resolver selection; use
  `dig @10.77.0.1 imac.meldnet.internal` for an explicit server test.
- **Linux with systemd-resolved's stub:** configures only the Meldnet link through
  D-Bus, using route-only private/reverse domains and disabling default-route DNS.
  It does not change other interfaces' DNS settings.
- **Other Linux setups:** starts an embedded proxy at `127.77.0.1:53` and journals
  a temporary `/etc/resolv.conf` change. Private queries go only to the primary;
  other queries go to the original nameservers. Search/options lines are preserved.
  There is no new runtime executable or service dependency.

Disconnect and graceful shutdown restore owned resolver settings. Startup recovers
file-based settings left by a crash before reconnecting. External administrator
changes are preserved; modified settings that still contain Meldnet's marker cause
an explicit error with the recovery journal retained. If another network manager
replaces the fallback resolver file while running, DNS drift is reported; disconnect
and reconnect to configure against the new upstreams.

On Linux's embedded-proxy fallback, killing the daemon can interrupt ordinary DNS
until it is restarted and recovers `/etc/resolv.conf`. Keep the same state directory
and run the daemon under a restarting service for unattended use. Applications
using their own DNS-over-HTTPS servers may bypass private system DNS. Simulation
never installs host DNS settings or starts a DNS listener.

### Manually connect two devices

1. Start the daemon and TUI on both devices. Pick a private range that does not
   overlap either device's existing networks; the example uses `10.77.0.0/24`.
2. Initialize device A with `10.77.0.1/24`, device B with `10.77.0.2/24`, and UDP
   listen port `51820` on both.
3. Exchange their **public** keys. They appear in the TUI and `meldnet key` output.
4. On A, add B's public key, allowed IP `10.77.0.2/32`, and B's reachable endpoint
   such as `192.168.1.20:51820`. On B, add A's key and `10.77.0.1/32`.
5. If B receives connections from a roaming A, B's endpoint for A can be blank;
   A must have a reachable endpoint for B. A keepalive of 25 seconds can preserve
   an existing NAT mapping. A server behind NAT needs a UDP port forward.
6. Press **c** on both, then `ping 10.77.0.2` from A. Check handshake and transfer
   status. `UP` alone means an interface exists, not that the peer is reachable.

Allowed IPs select traffic sent to a peer and constrain that peer's source
addresses. Use `/32` for a single IPv4 peer and `/128` for IPv6. The service rejects
overlapping routes between peers. Peer endpoints use `host:port` or `[IPv6]:port`.
Hostnames resolve when bringing the tunnel up; automatic endpoint refresh is not
implemented. Using both endpoints on the same reachable LAN is the easiest first
test. Existing host firewall rules still apply.

Default routes (`0.0.0.0/0`, `::/0`) install two `/1` routes per address family,
leaving the original default route intact. Endpoint host routes keep encrypted
UDP on the physical path. Full tunnels require explicit, stable endpoints for
every peer; endpoint roaming and physical-network changes require reconnecting.
Each routed IP family needs a local interface address. Existing routes are never
replaced; conflicts fail and roll back the connection attempt.

Only AllowedIPs prefixes create routes: an address such as `10.77.0.1/24` does not
automatically route the entire subnet. Manual mode does not provision gateway
forwarding, NAT, DNS, or kill-switch rules. Use host routes for the initial setup.
Subnet/exit routing requires separate gateway administration.

### TUI keys

| Key | Action |
|---|---|
| n | Open network list (c/d connection, a auto-start, + join, Enter peers) |
| j | Join with an enrollment key (unconfigured/pending device) |
| Enter / i | Initialize a manual configuration |
| c / d | Connect / disconnect |
| s | Edit node name, addresses, listen port |
| a / e | Add / edit a peer |
| x, then y | Remove the selected peer |
| ↑ / ↓ | Select a peer |
| r | Refresh |
| Tab / Shift+Tab | Move between form fields |
| Enter | Next field; save on the final field |
| Ctrl+S / Esc | Save / cancel a form |
| q / Ctrl+C | Quit the TUI; VPN stays running |

Manual mode only: disconnect before changing settings. Configuration revisions prevent one client
from overwriting another client's edits. If a save reports a stale revision,
cancel the form and reopen it after refresh.

## Headless operation

The CLI needs no terminal and uses exactly the same API:

```sh
./bin/meldnet init --name laptop --addresses 10.77.0.1/24 --port 51820
./bin/meldnet key
./bin/meldnet status
./bin/meldnet config > settings.json
# Edit the settings object, preserving the exported revision.
./bin/meldnet apply settings.json
./bin/meldnet up
./bin/meldnet down
```

Initialization is one-time; it refuses to overwrite an existing identity. Add a
peer before connecting. `config` exports public settings only, suitable for
editing or automation. `apply -` reads from stdin. Put `--socket` before the
subcommand. `status` emits JSON, including an `error` field if tunnel observation
fails; scripts should check that field as well as `tunnel.up`.

## Background services

Templates and installation instructions are in [docs/services.md](docs/services.md).
Installing a service is optional and is not done by `make build` or the TUI.
Gracefully stopping the daemon tears down all its tunnels. Each profile reconnects
after startup only if auto-connect is enabled. Legacy manual profiles default to
off and legacy managed profiles to on. A daemon crash closes the embedded tunnels
too; the OS removes their non-persistent TUNs and attached routes. Startup removes
surviving endpoint routes using each profile’s private recovery journal.
Keys/settings survive. Failed cleanup remains visible and can be retried with
`meldnet down`.

When upgrading from the earlier wg-quick backend, disconnect and stop the old
daemon before replacing it. Existing `node.json` identities/settings are reused.
The new daemon refuses leftover `runtime/meldnet.conf` state rather than taking
over a potentially live legacy tunnel. The old `--wg` and `--wg-quick` options
have been removed.

## Verification

```sh
make check          # formatting, vet, race-enabled unit/API/TUI tests
make cross          # macOS + Linux, Intel + ARM
make integration    # manual IPv4/IPv6 plus primary/two-client traffic in containers
python3 scripts/smoke-tui.py  # actual terminal app, temporary simulated daemon
python3 scripts/smoke-managed-tui.py  # masked enrollment and device directory
./scripts/test-dns-linux.sh  # isolated Linux DNS and D-Bus protocol tests
```

The integration test needs Docker with `/dev/net/tun` available. Its test image
has no WireGuard packages and runs the daemon with an empty executable search
path. Only test containers receive NET_ADMIN plus TUN access on an isolated
internal bridge. Driver helpers such as jq, dig, and the test D-Bus daemon are not application dependencies.
Tests cover UID authorization, key isolation, IPv4/IPv6 traffic, handshakes,
counters, full-tunnel endpoint protection, rollback, crash recovery, identity
persistence, reconnect, and teardown. It does not use host
networking or install anything on the host. The reusable image remains cached;
containers and their network are removed on exit.

Native macOS encrypted traffic still needs validation with an actual peer.
Darwin ioctl layouts, route encoding, and native read-only route lookup are
tested locally alongside the TUI/API tests and cross builds. These do not replace
privileged utun address/route setup and traffic testing.

## Development continuity

Read [AGENTS.md](AGENTS.md), [handoff.md](handoff.md), and
[docs/architecture.md](docs/architecture.md) before changing the project.
`agent.md` points to the canonical rules. `handoff.md` records the current
milestone, decisions, verified evidence, limitations, and next steps.

Protocol details: [docs/api.md](docs/api.md). Upstream references:
[Fiber](https://docs.gofiber.io/),
[Bubble Tea](https://github.com/charmbracelet/bubbletea), and
[wireguard-go](https://git.zx2c4.com/wireguard-go/).
