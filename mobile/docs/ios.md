# iOS native owner

The Flutter app is a client of the native `si.meld.meldnet/v1` adapter.
`PacketTunnel.appex` is a real Network Extension target embedding upstream
WireGuardKit and its wireguard-go C archive. It generates identities, persists
credentials before registration, performs pinned TLS control requests, installs
one private-pool route, reconciles membership, and owns tunnel lifetime. Closing
the Flutter UI does not explicitly stop the extension. OS termination destroys
the userspace tunnel; background and force-quit behavior still require device
acceptance.

## Build

Requires macOS, Xcode, Flutter dependencies resolved for `mobile/`, and Go on
PATH (the build also searches `/opt/homebrew/bin` and `/usr/local/bin`). iOS 15 is
the deployment minimum. Use a Flutter SDK satisfying the checked-in Dart
`^3.13.1` requirement (Flutter 3.47.7 / Dart 3.13.5 was verified).
Desktop CGO-disabled builds are unchanged: Apple's
embedded WireGuard bridge necessarily uses C linkage.

From the repository root:

```
mobile/ios/bootstrap-wireguard.sh
mobile/ios/test-native.sh
cd mobile
flutter pub get
flutter build ios --release --no-codesign
```

The bootstrap checks out upstream `wireguard-apple` at
`2fec12a6e1f6e3460b6ee483aa00ad29cddadab1` from
`https://git.zx2c4.com/wireguard-apple`. `wireguard-package.patch` changes only
its PackageDescription tools version (5.3 to 5.5, needed by its declared iOS 15
constants), includes `sys/types.h` explicitly for Xcode 26 modules, and declares
the native control C ABI and build dependencies. Bootstrap copies the exact
shared `mobile/native/control/control.go` into the upstream Go bridge as a
subpackage plus a small C shim, without importing Android dependencies or
changing upstream WireGuard module versions. No
cryptographic code is changed. The fetched checkout is ignored by Git; review
and preserve the upstream license when distributing. Bootstrap rejects extra
tracked modifications beyond the recorded patch. The Xcode build invokes the
upstream Go Makefile, including its runtime timer patch; it never executes a VPN
CLI at runtime.

Runner's build depends on PacketTunnel, embeds its `.appex`, and links the
upstream Swift package and generated `libwg-go.a`. Both targets have explicit
Network Extension and shared Keychain entitlements. Neither target selects a
developer team or provisioning identity. For deployment, assign your own team,
bundle identifiers, matching keychain access group, and packet-tunnel profiles
for **both** targets. The app bridge's provider bundle identifier must match
PacketTunnel. Unsigned builds cannot be installed on an iPhone.

## Security and behavior

- Only profile IDs enter `providerConfiguration`. The app stages the enrollment
  intent in shared Keychain; the extension creates each membership's WireGuard
  identity and device credential. After-first-unlock, device-only Keychain items
  support background reads without iCloud synchronization or plaintext exports.
- The native bridge and extension validate independently. Enrollment is HTTPS
  origin-only, certificate-SHA256 pinned, TLS 1.3, no redirects and no proxy.
  Apple system URLSession TLS could not negotiate the primary's Ed25519
  certificate in an actual loopback test. The provider therefore uses the shared
  maintained Go TLS implementation through its existing Go archive. Exact pin
  and validity dates establish identity, matching desktop, without a public-PKI
  hostname/SAN requirement. Native Swift supplies system-resolved numeric
  underlay addresses and rejects control destinations inside the private pool
  or names inside `meldnet.internal`. Resolver waiting is bounded to 6 seconds
  with cancellation checks every 100ms (an underlying OS lookup may finish later
  on its worker). Go HTTP/TLS has an 18-second total timeout and a 128KiB body
  cap. A cancelled C call may finish within that deadline; generation guards
  prevent a late result from writing state or starting a tunnel. Public projections omit origin,
  pin, invite, device credential and private key. Diagnostics are sanitized.
- Registration first tries the durable device credential, recovering a lost
  successful registration response. A known post-enrollment 401 cancels the
  tunnel, even if Keychain is temporarily unwritable. Other control outages retain
  an already running valid configuration and back off to 60 seconds. Snapshot
  member evidence becomes stale after 15 seconds without successful control.
- One membership may be active. Switching requires disconnect. `join` starts the
  extension and may prompt for system VPN consent; when `connect` is false it
  registers and stops before starting WireGuard or installing VPN routes.
  Enrollment failures preserve pending identity for retry. Auto-connect and
  OS on-demand are not implemented and the bridge advertises that limitation.
- Split DNS uses the primary VPN IP with an explicit /32 DNS-server route,
  `domainName` and `matchDomains` for `meldnet.internal` and
  the covering private reverse zones, and no all-domain DNS setting or public
  fallback proxy. Public DNS remains the system resolver's responsibility.
  DNS settings are rebuilt and validated on every WireGuardKit publication,
  including handover; matching does not install bare-name search domains.
  Public snapshots say `configured`, not `ready`: a stored configuration does
  not prove a successful iPhone lookup.
  The primary VPN authority serves its `host.default.meldnet.internal` aliases
  as well as legacy `host.meldnet.internal` names after the primary is updated.
  Aliases based on a mobile-local membership ID are not implemented. Reverse matching covers /24
  zones for smaller pools, so unrelated PTRs in the same /24 go to the private
  authority and may return NXDOMAIN. No host resolver files are touched.
- `up` is an OS VPN interface status, not a reachability claim. Control evidence
  describes a refreshed membership list, not a peer handshake. Per-peer
  handshake/traffic evidence is not implemented and never fabricated.

## Verification and remaining acceptance

`mobile/ios/test-native.sh` compiles and runs Swift checks for origin/material
validation, injection rejection, enrollment size limits, snapshot secret
isolation, desktop-compatible profile ID boundaries, and native network
validation (public/default routes, out-of-pool/broadcast IPs, duplicate members,
endpoint credentials, and DNS escape). It also checks actual NetworkExtension
settings objects for the primary host route, split forward/reverse matching,
reapplication, older-primary compatibility and configured/inactive status.
It touches neither Keychain nor host network settings.

`mobile/ios/test-https.sh` builds the production C shim/shared Go transport and
Swift transport adapter on macOS, then exercises real TLS 1.3 against isolated
loopback Go servers with primary-style Ed25519 CN-only certificates. Success,
wrong pin, expired/future certificate, redirect refusal and streamed response
bounds pass. Synthetic private keys stay in server memory; no production
identity, external DNS, host routes, VPN or device is used. This verifies the
native transport on macOS, not iOS device behavior.

On 2026-10-05 these unsigned Xcode 26.3 builds succeeded, including the embedded
extension and real WireGuard archive:

```
xcodebuild -project mobile/ios/Runner.xcodeproj -scheme Runner -sdk iphonesimulator -configuration Debug -derivedDataPath /tmp/meldnet-ios-derived CODE_SIGNING_ALLOWED=NO ARCHS=arm64 build
xcodebuild -project mobile/ios/Runner.xcodeproj -scheme Runner -sdk iphoneos -configuration Release -derivedDataPath /tmp/meldnet-ios-device CODE_SIGNING_ALLOWED=NO ARCHS=arm64 build
```

The final `make mobile-ios` also succeeded after the Go TLS integration and
produced `mobile/build/ios/iphoneos/Runner.app` (23.7MB reported by Flutter) and
`bin/Meldnet-ios-unsigned.zip`. Final native Swift and loopback Swift/C/Go transport
tests passed. The latter also checks private control DNS rejection and cancelled
requests.

No developer identity was used, no app was installed, and no VPN was connected.
These builds do not satisfy device acceptance. Still required before release:
signed iPhone enrollment against the existing primary, TLS wrong-pin/expired
certificate cases, lost registration responses, revocation and Keychain failure,
real encrypted and relayed traffic, UDP/TCP private DNS and NXDOMAIN no-leak
capture, public DNS preservation, consent denial, owner/UI process death,
background lock/unlock and Wi-Fi/cellular handover. Also verify memory/battery
behavior, IPv6-only underlay, enrollment-only extension lifetime, and App Store
entitlement/review requirements. The app must not be called production-ready on
build evidence alone.
