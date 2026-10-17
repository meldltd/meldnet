# Meldnet handoff

Updated: 2026-10-17

## Current milestone

Mobile DNS follow-up: the user reports the updated signed iPhone build 2 works
following installation/launch and the requested `imac.meldnet.internal` retest.
Host rows now show VPN IPs. This is user-reported physical-device acceptance,
not an independently captured DNS test. Primary-profile-qualified DNS aliases
are implemented in source but still require primary deployment. The earlier
relay-performance milestone below remains unfinished.


Relay performance investigation: the user reports a remote Linux primary with
macOS clients, 100–200 kbps through Meldnet versus 10–15 MB/s directly between
the Macs (confirmed by the user; not a Mac-to-primary measurement). The full
400–1,200x gap (if kbps literally means kilobits/s) is NOT reproduced or resolved.
A confirmed batching inefficiency is fixed; isolated encrypted Linux throughput
improves about 48–56%. Native deployment diagnosis is the next step.

Independent multi-network profiles remain implemented across daemon/API/CLI/TUI
and macOS menu app. This milestone includes experimental Windows daemon/CLI/TUI
support, a native Windows Win32 desktop/tray app, and a Linux GTK desktop/tray
frontend. The initial Flutter mobile client is implemented for Android/iOS with
separate native VPN owners, one active membership and multiple saved memberships.
Mobile remains experimental and is not store-ready; physical-device lifecycle,
handover and signing acceptance remain explicit release gates.

## Mobile host IPs and DNS follow-up (2026-10-17)

- User reports only private iPhone names fail (`smt.meldnet.internal` and
  `smt.default.meldnet.internal`); the same host's VPN IP works and public DNS
  continues to work. Follow-up confirms both forms resolve on desktop, iPhone
  displays `ready`, and the real name `imac.meldnet.internal` fails on iPhone.
- Shared Flutter host rows now display both hostname and a labeled VPN IP,
  including address-only records. Existing hostname copying and evidence labels
  are preserved. Widget expectations and narrow-screen/large-text fixtures now
  include peer IPs; neither Flutter nor its bridge receives new private data.
- Confirmed independent DNS bug: desktop aggregate DNS served profile-qualified
  names, but the primary's VPN authority did not. The primary slot now supplies
  its local profile qualifier to its authority, serving both `host.meldnet.internal`
  and `host.default.meldnet.internal`. Local aggregate inputs remain unqualified,
  avoiding double qualification and preserving cross-profile ambiguity rules.
  This requires updating the PRIMARY binary, not just the iOS app. No remote
  deployment, personal VPN connection, host resolver/routing changes or signing
  was performed. Mobile-local membership aliases remain unsupported.
- Regression tests exercise the actual slot-to-authority configuration over
  loopback UDP/TCP, both spellings, AAAA/HTTPS NODATA, private NXDOMAIN, public
  REFUSED, enrolled-source admission, removal, qualifier validation and unchanged
  legacy PTRs. Integration coverage adds qualified queries over encrypted tunnels
  and confirms qualified aliases disappear on revocation.
- Exact verification: `gofmt -w internal/privatedns/zone.go
  internal/privatedns/hub.go internal/privatedns/multi_test.go`; final
  `make check cross`: PASS (`go vet ./...`, `go test -race ./...`, format/isolation
  guards, daemon/CLI Darwin/Linux/Windows amd64/arm64 and both Windows GUI builds).
  Cross-compilation is not native device testing. `make mobile-ios-test`: PASS
  (native Swift validation/secret isolation and real loopback Swift/C/Go TLS).
- Earlier `make mobile-check` and `make mobile-ios`: BLOCKED by installed Dart 3.12.2
  versus checked-in `sdk: ^3.13.1`; superseded by the successful isolated-SDK
  build in the iOS follow-up below.
  Repository SDK requirement and lockfile were preserved. In a temporary source
  copy `/var/folders/n1/0_7rfpld6kb1y8p91d2yxjd40000gn/T/meldnet-mobile-check-6yj4c370`,
  changed ONLY the temporary SDK bound to ^3.12.2, then ran
  `flutter pub get && flutter analyze && flutter test`: PASS, no analyzer issues,
  7 tests. That temporary resolution chose six older SDK test dependencies, so
  this is compatibility evidence, not validation with the repository lockfile.
  Formatting-only changes outside the edited UI/test blocks were removed later.
- In `mobile/native`, `go vet ./...` passed. The first `go test -race ./...`
  stalled and was terminated; a repeat completed PASS (control and dnsbridge).
  A bounded targeted DNS policy/no-fallback run also passed:
  `go test -race ./dnsbridge -run 'TestPolicy|TestPrivateMissAndFailureNeverFallThrough|TestPublicUsesUnderlay|TestPrivateWithoutVPNNeverDials|TestInvalidJSONClearsPartialPolicy' -timeout 30s`.
- `bash -n scripts/integration-primary.sh`: PASS. The direct
  `bash scripts/integration-primary.sh` attempt was interrupted while Docker
  stalled fetching Alpine metadata (transport reported only one connection
  allowed), before containers/tunnels were created. Offline fallback verification
  uses existing `meldnet-integration:local` as the base, copies the final rebuilt
  Linux arm64 CLI/daemon into task image `meldnet-dns-check:local` with
  `docker build --network=none`, and runs the same integration script via Python
  with only its build line omitted and image tag/cwd substituted. PASS on the
  final run: real encrypted relay, direct primary UDP/TCP legacy AND qualified
  names, PTR/system DNS, private NXDOMAIN/public REFUSED, original upstream
  preservation, resolver cleanup/crash recovery and removal of both aliases on
  revocation. The first offline attempt failed on a new test-driver empty-array
  expansion under macOS Bash 3 with `set -u`; replaced that loop with two explicit
  queries before the passing run. Containers/networks cleaned by the test trap;
  task image removed afterward. This is Linux-container evidence, not iOS traffic.
- Final `git diff --check`: PASS. No commit/push; older mobile packages remain
  unchanged. The diagnostic questions were answered in the follow-up below.

## iOS DNS routing follow-up (2026-10-17)

- The user's follow-up confirms `imac.meldnet.internal` fails on iPhone while
  the VPN IP works; both name forms resolve on desktop; iPhone displays `ready`.
  Read-only public local API status/peers showed this Mac already connected to
  the same network. No tunnel was started/stopped and no routes/resolver files
  were modified. Direct `dig +time=2 +tries=1 @10.77.0.1 imac.meldnet.internal A`
  and its `+tcp` variant both returned `10.77.0.2` from the live remote primary.
  Direct `imac.default.meldnet.internal` returned authoritative NXDOMAIN, while
  `dscacheutil -q host -a name` resolved both forms through macOS local DNS.
  Thus the alias bug is confirmed on the deployed primary; primary DNS is
  reachable from this Mac, but that does not establish the iPhone's DNS path.
- Candidate iOS correction: on every WireGuardKit network-settings publication,
  the production validator applies the private DNS domain and an explicit /32
  included route to the primary resolver, preserving the broader VPN routes.
  Only private forward/reverse matchDomains are installed; no empty match,
  public override, public fallback or bare-name search domain is added. Settings
  reapplication deduplicates the resolver route. This hardens resolver routing;
  physical iPhone success is still UNVERIFIED and the root cause is not proven.
- iOS public `dnsState` is now `configured` instead of `ready`: the saved primary
  DNS declaration is not evidence of a successful OS lookup. Documented in
  bridge-v1 and the iOS README. Android status behavior is unchanged.
- Native Swift tests instantiate actual NetworkExtension settings objects and
  exercise the production helper: primary-only resolver, domainName, forward and
  reverse matches, /32 route, repeated publication, /16 /20 /28 pool boundaries,
  older primary without DNS, unsafe network refusal and honest public status.
- `make check cross mobile-ios-test`: PASS (root vet/race/format/isolation checks,
  all desktop cross-builds, native Swift checks including DNS settings and the
  Swift/C/Go TLS loopback tests). These are macOS-hosted tests and builds, not an
  actual iPhone DNS or handover test.
- Build-tool recovery complete: installed isolated official Flutter 3.47.7
  (Dart 3.13.5) under ignored `bin/toolchains/flutter-3.47.7/flutter`; verified
  downloaded archive SHA256 against official macOS release metadata:
  `dd9da574c3168460550117eb0e8dd0313ae934fabf64a82a6d557fff5fd1422e`.
  Removed the downloaded ZIP after extraction. Global Flutter install and the
  checked-in SDK requirement and lockfile remain unchanged.
- `/Users/marhi/meldnet/bin/toolchains/flutter-3.47.7/flutter/bin/flutter pub get --enforce-lockfile`
  in `mobile/`: PASS. Then
  `PATH="/Users/marhi/meldnet/bin/toolchains/flutter-3.47.7/flutter/bin:$PATH" make mobile-check mobile-ios`:
  mobile-check PASS (no analyzer issues, all 7 Flutter tests, native Go vet/race
  tests). Initial device build failed because Apple's callback passes base
  NETunnelNetworkSettings but the helper takes NEPacketTunnelNetworkSettings.
  Added an explicit checked cast, failing safely for an unexpected settings type.
  Final same-PATH `make mobile-ios-test mobile-ios`: PASS, including the full
  release device build with embedded PacketTunnel extension.
- Updated `bin/Meldnet-ios-unsigned.zip`: 10,061,594 bytes; Flutter reports
  Runner.app 24.3 MB. ZIP integrity and presence of both app and extension
  binaries: PASS. `lipo -archs` confirms both arm64; `codesign -dv` confirms
  app and extension are unsigned. ZIP SHA256:
  `e96e41af7e627e4d8bd7509be5b7ec543b86fa512b5b11a561de552221f61d10`.
  Android artifacts are unchanged. No signing, device installation, remote
  deployment, commit or push occurred. Final `git diff --check`: PASS.

The signed installation/launch is now recorded below. Next: on the iPhone,
retest `imac.meldnet.internal`, private NXDOMAIN/public DNS and Wi-Fi/cellular
handover. If the shorter name still fails, capture the iPhone's DNS packets and
system resolver state; the explicit-route/domain change is a candidate fix,
not a proven root cause. Deploy the primary alias fix via an authorized
workflow for `imac.default.meldnet.internal`. Use the isolated Flutter PATH
above for reproducible subsequent builds. Do not claim the iPhone issue
resolved based on Mac DNS probes, settings-object tests or unsigned builds.

## Connected iPhone installation (2026-10-17)

- User explicitly connected the phone and requested running the updated app.
  The sole connected device was the paired iPhone 15 Pro (iOS 27.0.1), with an
  existing `si.meld.meldnet` development app build 1. No app uninstall or private
  app/Keychain data read/export was performed.
- Built and automatically provisioned a signed Release build for the connected
  device, using existing valid Apple Development identity/team K74N347VA8.
  Xcode fetched/generated app and PacketTunnel profiles as needed. Signing/team
  overrides were command-line only; generic checked-in project signing remains
  unchanged. App and extension both use build 2 to distinguish the candidate.
- Exact build: isolated Flutter PATH from above, then
  `xcodebuild -workspace mobile/ios/Runner.xcworkspace -scheme Runner -configuration Release -destination id=00008130-001A2D603C0A001C -derivedDataPath /Users/marhi/meldnet/bin/ios-device-signed -allowProvisioningUpdates -allowProvisioningDeviceRegistration DEVELOPMENT_TEAM=K74N347VA8 CODE_SIGN_STYLE=Automatic 'CODE_SIGN_IDENTITY=Apple Development' FLUTTER_BUILD_NUMBER=2 CURRENT_PROJECT_VERSION=2 build`:
  PASS. Output/log are ignored under `bin/ios-device-signed` and
  `bin/ios-device-signing.log`. No source changes in this installation turn.
- `codesign --verify --deep --strict` on the signed Runner.app: PASS. Verified
  app AND PacketTunnel profiles include this device/team; both build numbers are
  2 and both signed entitlements contain packet-tunnel-provider plus
  K74N347VA8.si.meld.meldnet.shared Keychain access. Existing memberships were
  not inspected; persistence is not claimed solely from successful installation.
- `xcrun devicectl device install app --device 658A4C6E-B31C-5722-AB9C-EAEDEDC32B08 --timeout 120 /Users/marhi/meldnet/bin/ios-device-signed/Build/Products/Release-iphoneos/Runner.app`:
  PASS, updated existing bundle in place. Then
  `xcrun devicectl device process launch --device 658A4C6E-B31C-5722-AB9C-EAEDEDC32B08 --timeout 30 si.meld.meldnet`:
  PASS, iOS confirmed launch. This establishes installation/launch, not physical
  DNS success, restored membership visibility, or a running VPN tunnel.
- No device VPN connect/disconnect, primary deployment, or Mac networking change
  was performed in this installation turn. Earlier unsigned ZIP remains unsigned;
  the separately signed on-device build is under the derived-data path above.

The user's successful follow-up is recorded below. Remaining acceptance includes
private NXDOMAIN/public DNS and handover. Primary deployment remains required for
`imac.default.meldnet.internal`.

## User iPhone acceptance (2026-10-17)

- After build 2 was installed/launched and the user was asked to reconnect and
  retest `imac.meldnet.internal`, the user replied "ok works". Record the iPhone
  candidate as working by user report. No additional test commands were run.
- This confirms the user's reported success after the DNS routing/domain changes
  and host-IP display update; it does not independently establish the exact root
  cause or verify qualified aliases, NXDOMAIN packet privacy, public DNS,
  handover, process death or background lifecycle. No primary deployment occurred.
- Next: primary deployment for `*.default.meldnet.internal` and remaining mobile
  lifecycle/no-leak acceptance when requested. Do not repeat the resolved
  ordinary-name iPhone diagnosis absent a new failure report.

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

## Relay batching and performance verification (2026-10-14)

- Primary `relayTUN.Read` drains queued packets into the supplied batch, without
  waiting for a full batch. `BatchSize` preserves the native TUN value. Host-bound
  packets are grouped up to the underlying write limit, retaining Linux GRO and
  respecting single-packet Darwin/Windows writes. No peer topology, MTU, queue
  limit, authentication, TTL, or cryptographic implementation changed.
- Regression tests cover native write limits (1/4/128), partial read batches,
  packet contents/TTL, write error propagation, invalid read buffers and closed
  reads. Existing membership/TTL/checksum tests remain. All network unit tests
  use fakes; no host tunnel, DNS, routing, or service was changed.
- `gofmt -w internal/vpn/relay.go internal/vpn/relay_test.go
  internal/vpn/relay_benchmark_test.go`: PASS.
  Final `make check cross`: PASS, including `go vet ./...`,
  `go test -race ./...`, isolation/format guards, CGO-free daemon/CLI builds for
  Darwin/Linux/Windows amd64/arm64. Cross-compilation is not native testing.
- `bash scripts/integration-primary.sh`: PASS: actual encrypted relay across
  isolated client networks, enrollment, private DNS UDP/TCP/PTR, OS resolution,
  upstream preservation, cleanup/crash recovery and revocation. Containers only.
- Added `scripts/benchmark-throughput.sh`: disposable isolated primary + two
  clients, real WireGuard TCP, primary kernel forwarding disabled, daemon PATH
  without external tools. The benchmark uses test-only iperf3 and preserves JSON.
  `bash -n scripts/benchmark-throughput.sh` and `git diff --check`: PASS.
- Initial `BENCH_SECONDS=5 BENCH_OUTPUT=bin/throughput-before
  scripts/benchmark-throughput.sh`: PASS; direct 102900.7, primary 4265.5,
  relay 1531.3, reverse relay 1292.9 Mbit/s. First patched run with output
  `bin/throughput-after` overlapped builds and was discarded as a comparison
  (direct 61178.6, primary 2612.1, relay 778.5, reverse 500.2 Mbit/s).
- Clean patched run: `BENCH_SECONDS=10 BENCH_OUTPUT=bin/throughput-after-idle
  scripts/benchmark-throughput.sh`: PASS. Direct 102192.3, primary 6260.8,
  relay 2244.5, reverse relay 2313.0 Mbit/s. Forward relay 991 retransmissions.
- Clean old-code run used `GOFLAGS=-overlay=/var/folders/n1/0_7rfpld6kb1y8p91d2yxjd40000gn/T/meldnet-baseline-57c31bz4/overlay.json
  BENCH_SECONDS=10 BENCH_OUTPUT=bin/throughput-before-idle
  scripts/benchmark-throughput.sh`. Overlay replaced only relay.go with its
  committed HEAD contents; working-tree source remained patched. PASS on retry:
  direct 102235.7, primary 4607.6, relay 1512.5, reverse 1484.0 Mbit/s; forward
  relay 750 retransmissions. First attempt failed because the running shell
  script was edited during execution (line offset changed); no results claimed
  from it. Final `make check cross` restored patched binaries after the baseline.
- These are individual 10-second local Docker bridge runs, not repeated WAN or
  native Mac benchmarks. Virtual direct rates do not represent physical NIC
  capacity. Retransmissions remain; no claim that batching eliminates loss.
- `go test ./internal/vpn -run '^$' -bench BenchmarkRelayTransit -benchmem
  -count=3`: PASS. Final bookkeeping-only runs: 29.7/30.3/33.9 us per burst of
  128x1200 bytes; 163840 B and 128 allocations per burst. This does not measure
  encryption or demonstrate a bookkeeping speedup; throughput evidence is above.
- `docs/performance.md` documents path comparisons and isolated reproduction.
  The daemon binaries were rebuilt, but existing installer/ZIP/tar packages were
  NOT regenerated for this change, and the user's remote primary was NOT updated.

Next: measure each Mac to the remote primary's public address and VPN address,
in both directions with the same TCP workload, then Mac-to-Mac VPN. Capture
latency, retransmissions, primary CPU and interface drops. The direct test was
Mac-to-Mac, so it does not measure the remote relay path. Transfer application
and kbps/KBps units still need confirmation. Deployment
of the patched primary and native Mac/WAN validation remain unfinished.

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
