# Meldnet handoff

Updated: 2026-10-04

## Current milestone

Independent multi-network profiles are implemented across the daemon, local API,
CLI, Bubble Tea TUI and native macOS menu app. Multiple non-overlapping tunnels can
run concurrently. Each profile persists a separate auto-connect preference;
connection/disconnection does not change that preference. Equal and nested IPv4
and IPv6 ranges are blocked atomically in the daemon, with actionable warnings.
The latest universal installer is `bin/Meldnet-0.1.0-universal.pkg`.

## Durable architecture

- `meldnetd` alone owns private keys, enrollment, persistence, embedded wireguard-go,
  TUNs, route recovery and DNS. No daemon subprocesses or presentation imports.
  macOS and Linux releases retain CGO_ENABLED=0.
- Unix HTTP v1 is protected by kernel peer credentials, a 0600 socket, allowed UID
  and root, Host validation and Origin rejection. CLI/TUI/menu use only this API;
  quitting a frontend leaves VPNs running. No keys in public responses or logs.
- `internal/profiles` stores version-1 `profiles.json` with up to 16 profiles.
  `default` uses the unchanged legacy root state; others use `profiles/<id>`.
  Migration preserves identity/enrollment and legacy startup policy (managed on,
  manual off). IDs are DNS-safe lowercase names, 1–31 characters. No profile
  deletion/renaming UI exists yet. Only default can host a primary; additional
  profiles enroll as clients.
- One process networking owner holds the global lock; each profile has its own
  WireGuard engine, TUN and route journal. Linux uses hashed interface names;
  Darwin uses separate utun devices. The coordinator reserves pools, interface
  networks and AllowedIPs; failed cleanup retains reservations. Crash recovery
  remains conservative and never claims the userspace tunnel survives death.
- Auto-start profiles are attempted in sorted order. Overlap failures stay paused;
  other startup failures can retry through control reconciliation. Manual up/down
  is independent from saved startup policy. Join can save enrollment then fail
  its connect-now step with 409; the user can switch networks without re-enrolling.
- One DNS hub owns host resolver changes. It serves a loopback aggregate at
  127.0.0.1:53 and per-primary authorities at their VPN addresses. Local names are
  `host.<profile>.meldnet.internal`; legacy `host.meldnet.internal` aliases work
  only when unique among connected profiles. Ambiguity is NXDOMAIN. Disconnect
  removes that profile’s records; the final disconnect restores the resolver.
  Configured private reverse pools remain private while another profile is active.
  macOS journals each reverse resolver file; Linux aggregate uses fallback proxy
  integration. Resolver edits preserve external changes and recovery journals.
- Managed networks remain trusted-primary relay networks, not direct mesh or
  end-to-end client encryption. Enrollment uses pinned HTTPS, hashed one-use keys,
  allocated IPv4 pools and authenticated member addresses. Membership changes can
  briefly recreate a tunnel. Private DNS never forwards private misses upstream.

## Controls and API

- `meldnet networks`; `meldnet --network work join --name laptop --key-file FILE`
  (or stdin), with `--auto-connect=false` and/or `--connect=false` as needed.
  `meldnet --network work up`, `down`, `autoconnect on|off`, `peers`, `status`, `tui`.
  Commands without --network target default; enrollment secrets are never args.
- TUI: n opens networks, arrows select, c/d connect/disconnect, a toggles startup,
  Enter opens peers, + opens masked enrollment with separate startup/connect choices.
  Async status results are scoped so late replies cannot cross selected profiles.
- API: GET /v1/networks, POST /v1/networks/join with
  {id,name,key,auto_connect,connect}, PUT /v1/networks/{id}/autoconnect with
  {auto_connect}. Existing v1 paths are scoped by replacing /v1 with
  /v1/networks/{id}. Unscoped paths retain default compatibility. Missing profile
  is 404 and range overlap is 409. Profile mutation JSON is limited to 16 KiB.
- Menu: per-network submenus show connection, startup checkbox, warning and peers;
  peers copy qualified hostnames. Join adds memberships independently. Status
  distinguishes interface/handshake/control evidence and simulation. The template
  icon adapts the first lowercase m and detached accent in https://meld.si.
- Open TUI opens the bundled CLI in Terminal as the authorized user, using the
  same socket, through a private self-removing command document. No sudo, Apple
  Events or daemon startup is involved. Shell metacharacter handling is tested.

## Installer and local state

- `make macos-installer` builds a universal macOS 13+ .pkg; `make
  macos-installer-test` checks archive and sandbox installation behavior.
  It includes the menu app, CLI, root daemon, boot LaunchDaemon and login agent.
- Installer requests native administrator authorization, sets secure root ownership
  and private state/log permissions, preserves the authorized UID on upgrade,
  loads absent jobs immediately and does not force a reboot or stop existing jobs.
  User shuts down a prior foreground daemon. CLI/TUI retain the same allowed UID.
- Package is a local development build: ad-hoc signed code, unsigned installer,
  not notarized. Signing identities are supported; production distribution still
  requires signing/notarization and disposable-Mac install testing.
- /Applications/Meldnet.app and root package files were observed on this laptop
  from an installation outside this turn. This multi-network update only rebuilt
  artifacts: it did not install/replace the host app, stop a host daemon, change
  host DNS/routes or connect a real host VPN. Install the rebuilt package to use it.
  An earlier session installed the user menu LaunchAgent. No reboot is needed.

## Verification of this milestone

- `gofmt -w cmd internal`; final `make check cross`: PASS. Includes go vet ./...,
  go test -race ./..., daemon dependency/subprocess checks and CGO-disabled builds
  for Darwin/Linux amd64/arm64. Cross-compilation is not native traffic validation.
- New race tests: independent enrollment/identities and persistent preferences,
  legacy migration, concurrent and nested IPv4/IPv6 overlap rejection, cleanup
  reservation retention, profile API isolation/validation/privacy, scoped TUI
  controls/stale-reply handling/masked enrollment, multi-network DNS separation
  and macOS temporary resolver-file recovery.
- `bash scripts/integration-linux.sh`: PASS. Real encrypted IPv4/IPv6, defaults,
  endpoint protection, UID isolation, crash recovery, identity persistence and
  teardown, with daemon PATH containing no tools.
- `bash scripts/integration-primary.sh`: PASS. Real enrollment, encrypted relay,
  revocation, UDP/TCP/PTR private DNS, OS resolution, upstream preservation and
  resolver recovery/cleanup.
- `bash scripts/integration-multinetwork.sh`: PASS (including final repeat). Two
  simultaneous encrypted networks, nested overlap rejection, scoped DNS and
  ambiguous-alias NXDOMAIN, independent down, auto-start policy, crash recovery
  and unchanged identity. Test restart waits for killed-process resource release.
- `bash scripts/test-dns-linux.sh`: PASS, including new aggregate DNS test and
  mocked systemd-resolved lifecycle. Docker was initially unavailable; started
  Docker Desktop and reran successfully. All networking tests use disposable
  NET_ADMIN/TUN containers, not host routes/DNS, and clean up on exit.
- `python3 scripts/smoke-tui.py` and `python3 scripts/smoke-managed-tui.py`: PASS
  in native PTYs: rendering, masked enrollment, directory, simulation labeling
  and quit independence. Multi-network controls additionally have focused tests.
- `make macos-test`: PASS. Swift compilation, legacy and scoped API fixtures,
  startup mutation, overlap error, profile decoding, privacy and fake TUI launcher.
- `make macos-installer-test`: PASS. Universal archive, authorization/no-restart,
  ownership/modes, signatures, UID gating; sandbox fresh/upgrade, immediate startup,
  existing job preservation, failures and filesystem attack checks.
- Earlier DNS test hit an ephemeral UDP/TCP test-port collision; isolated rerun and
  final suite passed. A new TUI test initially exceeded macOS Unix socket path
  length; it now uses a short temporary directory and passes.
- Native macOS encrypted VPN traffic and live system resolver activation have NOT
  been exercised. Darwin resolver tests use temporary paths; systemd-resolved uses
  a mock. Live Terminal opening/menu interactions have not been UI-tested for this
  milestone. Prior icon preview was rendered and inspected offscreen.

## Next actions and limitations

1. Validate installer upgrade, login/boot, multi-utun traffic, DNS and menu controls
   on a disposable Mac/VM with real peers; sign/notarize for distribution.
2. Shared local DNS requires 127.0.0.1:53 to be available; occupied port reports DNS
   failure. Linux fallback proxy death can interrupt ordinary DNS until recovery;
   supervise the daemon. No short-name search suffix is installed.
3. Replace membership-triggered tunnel recreation with live peer reconciliation.
4. Add profile removal/rename, credential rotation and address reuse as separate
   features. Primary TLS lasts five years; back up complete state, not just keys.
5. Managed pools remain private IPv4 /16–/28. No exit nodes, NAT/firewall setup,
   kill switch, direct mesh/NAT traversal, ACLs or automatic underlay endpoint
   refresh. Primary availability/bandwidth bounds each managed network.

Repository remains uncommitted; no remote, commit or PR was created. Read this
file and docs/architecture.md before changes; update exact results and remaining
limitations before handing back. Never record enrollment or private-key secrets.
