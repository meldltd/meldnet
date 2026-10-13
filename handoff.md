# Meldnet handoff

Updated: 2026-10-05

## Current milestone

Independent multi-network profiles remain implemented across daemon/API/CLI/TUI
and macOS menu app. This milestone includes experimental Windows daemon/CLI/TUI
support, a native Windows Win32 desktop/tray app, and a Linux GTK desktop/tray
frontend. The initial Flutter mobile client is implemented for Android/iOS with
separate native VPN owners, one active membership and multiple saved memberships.
Mobile remains experimental and is not store-ready; physical-device lifecycle,
handover and signing acceptance remain explicit release gates.

## Mobile implementation checkpoint (2026-10-05)

- Added `mobile/` with shared Flutter membership/join/status/peer screens, masked
  enrollment, explicit connect/disconnect, observed-state/error handling and the
  Meld logo/palette from meld.si. Native owners retain private keys, credentials,
  enrollment, persistence and VPN lifecycle without a Flutter engine. Versioned
  MethodChannel accepts typed commands and returns public state only.
- Android 11/API30+: separate private foreground-service process, Keystore AES-GCM
  wrapping of atomic owner-only files, upstream GoBackend Java API plus pinned
  wireguard-go JNI. Build arm64-v8a/x86_64 with 16KiB ELF alignment. Private DNS
  intercepts synthetic 198.18.0.53 through upstream netstack; primary queries bind
  to the exact owned VPN Network, public queries bind to the underlay. Invalid or
  absent VPN policy fails closed. Strict named Private DNS is rejected. Closing
  a TUN never claims it survives owner death. No Android always-on support.
- iOS15+: native controller/Keychain and a real embedded Packet Tunnel extension,
  WireGuardKit/Go, primary-only split DNS/reverse zones. Enrollment is durably
  staged and registration owned by the extension. No signing identity is selected.
- Real enrollment uncovered system TLS incompatibility with the primary's
  Ed25519 certificate on both Android and Apple. The shared native `control`
  package uses standard Go TLS1.3, exact leaf SHA256/date validation, no proxy or
  redirects, bounded responses and a total18s deadline. Android resolves with a
  cancellable5s bound and binds sockets to the chosen underlay. Secrets remain
  inside native owners, not Flutter responses/logs or command arguments.
- Added `make mobile-check`, `mobile-native`, `mobile-android`, `mobile-ios-test`,
  `mobile-ios-simulator`, `mobile-ios` and regeneration of native launcher icons.
  Desktop backends and their CGO-disabled release pipeline remain unchanged.
- Verification completed so far: root `make check cross` PASS (Go vet/race tests
  and six daemon/CLI plus two Windows GUI cross-builds); `make mobile-check` PASS
  (Flutter analyze,7 widget/model tests, native Go vet/race tests); Android
  `:app:testDebugUnitTest :app:lintDebug` PASS (13 JVM tests; no lint errors after
  raising API minimum for owned-VPN verification;12 warnings for pinned dependency
  updates, legacy launcher shape and redundant resource qualifier). Native Android instrumentation
  PASS3 tests (JNI policy guard, encrypted Keystore persistence/tamper rejection,
  private Binder owner snapshot without Flutter).
- Isolated Android35 arm64 emulator with16KiB pages: actual LivePrimaryTest PASS1
  against disposable Docker primary, including enrollment, authenticated real
  WireGuard handshake, UDP/TCP private A/NXDOMAIN, public DNS and system resolver.
  Initial failures exposed inactive-cellular selection, TLS compatibility and a
  test-only Docker UDP port mismatch; all corrected. Tests touch only the fresh
  task-owned emulator/container, never host DNS/routes or a personal device.
- Final Android `make mobile-android mobile-check` PASS. APK native libraries hash
  match both newly built ABIs; every ELF LOAD segment has16KiB alignment. Rebuilt
  instrumentation on the final installed APK PASS4 tests (live primary plus3
  native tests). A first UDP datagram was intermittently lost during Android VPN
  activation; the test now waits for VPN selection and uses bounded standard UDP
  retransmission. Private/public TCP and system DNS also passed. Synthetic invite,
  task-owned emulator/AVD and disposable primary were removed after disconnect.
- Final `make mobile-ios-test` PASS: Swift protocol/profile/public-secret-isolation
  tests plus actual Swift/C/Go TLS1.3 loopback handshake with primary-style Ed25519,
  wrong-pin/expired/future refusal, redirect refusal, streamed response bound,
  private-zone DNS refusal and cancellation. Final iOS simulator build PASS after
  GoTLS integration. Final `make mobile-ios` PASS:23.7MB unsigned Runner.app and
  `bin/Meldnet-ios-unsigned.zip`9.3MB containing PacketTunnel.appex. Verified app
  and extension version0.1.0/build1 and actual Go transport symbols in the
  extension; codesign confirms no signing. No actual iOS tunnel test.
- Deliverables: `bin/Meldnet-android-debug.apk`139MB (development debug signature),
  `bin/Meldnet-ios-unsigned.zip`9.3MB (requires app+extension provisioning), and
  `bin/mobile-preview.png` (fixture-based UI preview, not connection evidence).
  `git diff --check` PASS. No commits/pushes or store distribution performed.
- Limits: no startup/on-demand/always-on, simultaneous active profiles, profile
  removal, mobile primary hosting, full-tunnel exit routing or desktop profile
  DNS aliases. Physical Android/iPhone handover, Doze/screen-lock/process death,
  permission/revocation and packet-capture leak acceptance remain unfinished.
  Next: provision app+extension for physical iPhone acceptance; exercise physical
  Android lifecycle and hostile/offline network transitions before distribution.

New artifacts: `bin/Meldnet-windows-amd64.zip`,
`bin/Meldnet-windows-arm64.zip` and `bin/Meldnet-linux-gui.tar.gz`.
Windows packages include official signed Wintun 0.14.1 DLLs and an explicit
PowerShell installer; optional NSIS setup executables are now built with
`make windows-installer` (see the latest checkpoint below). Linux payload includes GUI, desktop entry and optional
systemd definition/installer, with daemon binaries installed separately.
The previous universal macOS installer remains
`bin/Meldnet-0.1.0-universal.pkg`; this turn rebuilt the app but not that installer.
No host service was installed/started, real host VPN connected, or host DNS/routes
changed. Real encrypted traffic validation used disposable Linux containers and the
fresh Android emulator; no personal device or host network was used.

## Windows installer build addition (2026-10-05)

- Added `make windows-installer` (depends on `windows-package`) and
  `make windows-installer-test`. New artifacts:
  `bin/Meldnet-windows-amd64-setup.exe` and
  `bin/Meldnet-windows-arm64-setup.exe`. These are unsigned development builds.
- `scripts/build-windows-installer.py` validates each ZIP's exact seven-file
  payload, rejects duplicate/traversing/unexpected entries and oversized members,
  and checks every EXE/DLL's native PE architecture. It uses NSIS 3.08+ if installed
  (`MAKENSIS` override), otherwise the isolated Docker compiler defined by
  `scripts/windows-installer.Dockerfile`; `--docker` forces that path. Child build
  commands use argument arrays/timeouts. Compilation containers have no network.
- `windows/installer.nsi` requests administrator authorization, checks native OS
  architecture via NSIS IsWow64Process2-based macros, and requires the intended
  individual user's SID rather than assuming the elevated administrator is the
  VPN user. Silent installs are rejected. Only digit/hyphen SID arguments pass
  the wizard; the existing PowerShell installer resolves/validates the account.
- Setup protects its payload staging directory with a protected administrator/
  System DACL and administrator ownership before extracting scripts/binaries.
  It invokes native PowerShell with no profile and a 120-second inactivity
  timeout, reusing the signed-Wintun and existing-install refusal checks. It
  reports nonzero/timeout failures and does not delete partial state or services.
  Installation now also copies the Windows README into Program Files/Meldnet.
- Fresh install only: no uninstaller, shortcuts, implicit GUI launch/login job,
  or automatic upgrade/rollback. Manual removal/upgrade and state-preservation
  instructions are in the packaged README. Partial failure may leave files or a
  service; investigate rather than deleting identities/recovery journals.
- Verification: final `make windows-installer windows-installer-test`: PASS.
  It ran `make cross` for all daemon/CLI targets plus both Windows GUIs, rebuilt
  both ZIPs, compiled both setup EXEs with Docker NSIS 3.08-3+deb12u1, and passed
  all three Python test cases (both architectures, traversal/duplicates/extras,
  wrong architecture/malformed PE). No installer was executed.
- `make check`: PASS (`go vet ./...`, `go test -race ./...`, formatting and daemon
  isolation/subprocess guards; unchanged Go tests were cached). `python3 -m
  py_compile scripts/build-windows-installer.py scripts/build-windows-package.py
  windows/tests/test_installer.py`: PASS. `git diff --check`: PASS.
- Actual setup archive inspection: `7zz l` recognized NSIS 3 Unicode. Inline
  Python invoked `7zz x` in temporary directories for both setup EXEs: all seven
  extracted payload files matched their ZIP bytes, and each PE contained the
  requireAdministrator manifest. Extraction is NOT installer execution.
- Next: disposable Windows x64/ARM64 acceptance for UAC, intended-user SID,
  wrong-architecture rejection, staging ACLs, signature verification, native
  PowerShell path, existing-install refusal, startup/failure/timeout and manual
  recovery. NSIS build and archive checks do not establish native behavior.
  No host service, routing, DNS or tunnel was installed/changed in this turn.

## Windows desktop/tray addition (2026-10-05)

- Added `cmd/meldnet-gui` with native Windows controls, a Meldnet monogram tray
  icon, window show/hide, tray connect/disconnect, explicit quit, Explorer restart
  recovery and visible-window fallback when tray registration fails. `--tray`
  starts hidden; `--socket` selects a development pipe. No GUI autostart or shortcut
  is installed. Window close hides to tray; app quit never changes daemon state.
- Window provides independent network selection, connect/disconnect, saved
  service-startup preference, masked enrollment with separate connect-now/startup
  choices, public peer evidence and hostname copy. Enrollment clears on submit
  and close. API error bodies are never displayed, logged or persisted by the GUI.
  Requests run serially off the UI thread, capture the target profile, and refresh
  observed state after mutation failures. Failed reads discard stale profiles.
  Polling leaves enrollment inputs editable; mutations disable duplicate actions.
- Reuses the authenticated v1 named-pipe client. Added portable `internal/desktop`
  model tests for profile/action scoping, independent startup preference, ambiguous
  join recovery, error/secret privacy, stale snapshot clearing, and simulation,
  handshake versus control evidence (including future-timestamp rejection).
  The daemon dependency guard now excludes desktop/TUI presentation packages.
- Added `make windows-gui`; `make cross` and Windows packaging include the CGO-free
  GUI executable for amd64/arm64 with Windows GUI subsystem (no console window).
  Installer copies it alongside the existing tools; launching it never installs,
  starts, stops or elevates the daemon. No new framework/dependency was added.
- Verification: `gofmt -w cmd/meldnet-gui internal/desktop`; `make check cross`:
  PASS, including `go vet ./...`, `go test -race ./...`, isolation/format guards
  and all six daemon/CLI plus two Windows GUI cross-builds. Windows-specific
  `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...` and arm64 equivalent:
  PASS. Compile-time Win32 structure size checks pass for both architectures.
- `python3 scripts/build-windows-package.py`: PASS. Inline Python ZIP/PE checks:
  PASS for both packages (CRC, exact seven-file payload, GUI machine architecture,
  GUI subsystem=2, binary equality with build output and installer inclusion).
  `git diff --check`: PASS. No host service, tunnel, routes or DNS were changed.
- These are macOS-hosted model tests and cross-builds, NOT native Windows GUI or
  traffic tests. No Windows screenshot, interactive tray/clipboard/accessibility,
  Explorer recovery or installer execution is claimed. Native acceptance checklist
  is in `windows/README.md`; use a disposable Windows 10/11 VM next. System-DPI
  scaling is implemented; per-monitor scaling, native screen-reader behavior and
  visual inspection remain unverified. The GUI does not configure manual peers,
  issue invites, remove profiles or provide an Open TUI launcher. Prior encrypted
  container tests below belong to the previous milestone and were not rerun for
  this frontend-only change.

## Platform additions and verification (2026-10-05)

- Windows OS code is isolated in `_windows.go`. Local v1 HTTP uses a SID-restricted
  local named pipe, rejects remote clients and additional unprivileged instances,
  and authenticates the server's privileged owner before enrollment crosses IPC.
  Only an individual account SID is accepted. Protected owner-only DACLs replace
  Unix modes; reparse paths are rejected. File handles provide exclusive locks.
- Embedded wireguard-go/Wintun uses native IP Helper for IPv4/IPv6 addresses and
  routes; endpoint journals include LUID/name/next-hop and owned metric/protocol.
  SCM starts independently of a GUI and reports Running after API startup; stop
  and shutdown cancel the daemon and run cleanup. Windows releases keep CGO=0 but
  require the bundled driver DLL. No daemon subprocess or presentation import.
- Windows private DNS uses journaled, owned local NRPT rules for the private
  forward/reverse namespaces and loopback aggregate, preserving unrelated rules
  and external edits. Foreign local/Group Policy rules block activation.
  Native policy activation/cache invalidation needs Windows acceptance tests.
- Linux GUI uses GTK3/PyGObject and optional Ayatana/AppIndicator outside Go.
  It mirrors macOS per-network controls, masked join, independent startup, errors,
  evidence-based peer labels and public-hostname copying. It has a window fallback,
  serial background requests, bounded responses/timeouts, server peer credentials,
  stale-snapshot clearing and observed-state refresh after mutations. Quit leaves
  tunnels running. No Open TUI launcher is implemented in Linux GUI.
- `gofmt -w cmd internal`; final `make check cross`: PASS. Includes `go vet ./...`,
  `go test -race ./...`, formatting/subprocess/daemon-UI isolation guards and
  CGO-free daemon/client builds for Darwin/Linux/Windows amd64/arm64.
  Windows cross-compilation is NOT native test execution or traffic validation.
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...` and equivalent arm64:
  PASS, including Windows-specific test-source type checking. Added native tests
  for DACL privacy/exclusive locking/reparse refusal and named-pipe path/account
  validation/API exclusivity. They have NOT run on Windows; pipe test needs an
  elevated test process. Common API validation stays platform-neutral; Unix
  socket/resolver-file tests are correctly excluded on Windows.
- Portable DNS-policy fake tests: PASS in the race suite. Verify journal-before-
  write, partial-write crash recovery, external-edit preservation, cleanup failure
  and retained intent when resolver refresh fails. No test touches host DNS.
- `make linux-gui-test`: PASS on macOS (3 model tests; Linux-only peer-credential
  transport test skipped). Container `python3 -m unittest discover -s linux/tests
  -v`: PASS, all 4 including actual fake Unix HTTP framing, Host, size limits,
  sanitized enrollment errors, profile scoping and state evidence.
- Built `meldnet-gui-tests:local` from `scripts/linux-gui.Dockerfile` in Docker.
  `docker run --rm --init --network none -v /Users/marhi/meldnet:/work:ro -v
  /Users/marhi/meldnet/bin:/out -w /work meldnet-gui-tests:local xvfb-run -a
  dbus-run-session -- python3 scripts/smoke-linux-gui.py /out/linux-gui.png`: PASS.
  Real GTK widgets with an in-memory API fake: disconnect preserves startup,
  startup toggle, masked/cleared join, peer copy, invalidated stale snapshots and
  quit isolation. Screenshots `bin/linux-gui.png` and `bin/linux-gui-join.png`
  rendered and visually inspected. No real GUI-controlled VPN was claimed.
  Container lacks AT-SPI bus service, so accessibility-bus integration remains
  unverified; widget accessible names/keyboard controls use native GTK.
- Initial Docker Hub Python image pull stalled and was canceled; tests instead
  used the cached Debian base with GTK packages. Initial Xvfb wrapper stalled as
  container PID1; an explicit DISPLAY run passed, and final repeat with `--init`
  passed normally. Only the created test container was removed.
- `make macos-test`: PASS (Swift API/profile/privacy fixtures and safe TUI launcher).
- `bash scripts/integration-linux.sh`: PASS: actual encrypted IPv4/IPv6, defaults,
  endpoint protection, UID isolation, rollback, crash recovery, identity and
  teardown with no daemon tools on PATH. `bash scripts/integration-multinetwork.sh`:
  PASS: two encrypted tunnels, nested overlap rejection, scoped DNS, independent
  disconnect/startup, crash recovery and unchanged identities. Containers only.
- `make linux-gui windows-package`: PASS; final packaging rerun after source edits
  also PASS. ZIP CRC checks and expected binary/DLL/license/installer entries:
  PASS for amd64 and arm64. Wintun archive SHA-256 matched the official published
  hash. Linux tar contents checked. Windows PowerShell/Authenticode installer
  validation and service installation have NOT been executed on this Mac.
- `python3 -m py_compile` for GUI/smoke/packaging scripts; `sh -n
  scripts/install-linux.sh`; `git diff --check`: PASS. Impeccable mechanical
  detector on the GUI returned no findings; native screenshots inspected.
- Mobile assessment references Apple Network Extension, Android VpnService and
  upstream WireGuard embedding documentation. First milestone should be one
  active managed client membership, with platform-owned secrets and DNS. Android
  needs protected underlay sockets and an in-tunnel DNS proxy; iOS needs provider
  ownership and matchDomains. Signing, physical-device tests and native mobile
  builds remain future implementation, not checked-in applications.

## Immediate next steps and remaining platform limits

1. Disposable Windows VM: execute native tests and installer; verify authorized
   and unauthorized pipe users, LocalSystem state DACLs, real encrypted IPv4/IPv6,
   full-tunnel endpoint protection, Wintun removal on close/crash, route recovery,
   multi-profile isolation, NRPT activation/private misses/PTR/external edits,
   boot/shutdown and upgrade. Windows remains experimental until these pass.
2. Linux desktop distribution test: real GNOME/KDE/Wayland tray availability,
   clipboard/accessibility, login launch, installer/service definition and live
   daemon UI. Xvfb/fake API is functional/offscreen validation only. GUI depends
   on distro GTK/PyGObject; Go daemon stays headless and CGO-free.
3. Implement Flutter mobile screens with native VPN owners following
   `docs/mobile-platforms.md`, starting with the SDK/bridge feasibility spike; no
   iOS/Android release, app skeleton, mobile primary, or simultaneous mobile
   memberships is claimed. Native framework/JNI pipelines need separate builds.
4. Continue prior macOS native VPN, signing/notarization and lifecycle milestones
   below. Windows installer currently refuses existing services/files: upgrading
   requires an explicit administrator stop/preserve/replace/restart procedure.

## Durable architecture

- `meldnetd` alone owns private keys, enrollment, persistence, embedded wireguard-go,
  TUNs, route recovery and DNS. No daemon subprocesses or presentation imports.
  macOS, Linux and Windows Go releases retain CGO_ENABLED=0; Windows additionally
  needs the official signed Wintun DLL.
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

## Verification of prior multi-network milestone (2026-10-04)

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
