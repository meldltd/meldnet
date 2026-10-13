# Meldnet mobile

Flutter screens for Android and iOS, with native owners that keep VPN identity,
enrollment, persistence and tunnel lifetime outside the Flutter engine. This is
an initial development client, not a store release. A mobile device can save
multiple memberships but activate only one at a time. Disconnect before switching.
The trusted primary can see relayed client traffic; this is not a direct mesh.

## Build

Tested toolchain: Flutter **3.47.1**, Dart **3.13.1**, Go **1.27**, Android NDK
**28.2.13676358**, Java **21**, Xcode **26.3**. Android minimum is API 30 (Android 11),
with arm64-v8a and x86_64 builds. iOS minimum is 15.0.

From the repository root:

```
make mobile-check
make mobile-android
make mobile-ios-test
make mobile-ios-simulator
make mobile-ios
```

`mobile-android` builds the mobile JNI backend and a **debug-signed** APK at
`bin/Meldnet-android-debug.apk`. This is for development installation only.
`mobile-ios` builds an **unsigned** device app in `mobile/build/ios/iphoneos/`
and packages it as `bin/Meldnet-ios-unsigned.zip`;
it cannot be installed on an iPhone until both app and packet-tunnel extension
have your provisioning profiles, Network Extension capability and matching
Keychain groups. No developer identity or account is selected in the project.
`mobile-ios-simulator` builds the simulator application. The simulator cannot
establish production VPN behavior; build success is not encrypted traffic proof.

Android SDK defaults to `$HOME/Library/Android/sdk`; set `ANDROID_HOME` elsewhere.
Use `ANDROID_NDK_HOME` to override the NDK location. Native build dependencies
are downloaded on first use; lock files pin application dependencies. The desktop
Go release pipeline remains CGO-disabled. Android JNI and Apple's WireGuard
bridge use their separate native build pipelines. Mobile launcher artwork can
be regenerated on macOS with `swift scripts/render-mobile-icons.swift "$PWD"`.

## Use

Obtain an enrollment key from a trusted Meldnet primary, then choose **Join a
network**, enter a local network ID and device name, and paste the masked key.
The app clears enrollment input on submission. Registration persists native
identity and device credentials before contacting the primary, allowing a lost
registration response to recover without minting a second identity. After joining,
connect explicitly and approve the platform VPN permission prompt.

Screens show the observed tunnel interface separately from handshake or control
activity. A listed peer is not proof of reachability. Only public hostnames are
copied. Closing the UI does not send disconnect; terminating the OS VPN owner
still destroys its userspace tunnel. Startup/always-on/on-demand are not offered
until the platform adapters implement and validate those behaviors.

The desktop alias form `host.<profile>.meldnet.internal` is not implemented by
the first mobile client; use the primary's returned `host.meldnet.internal`
names. Mobile primary hosting, full-tunnel exit routing, simultaneous memberships,
profile removal and automatic startup remain out of scope for this version.

## Architecture and security

- [Bridge contract](docs/bridge-v1.md): typed public intent/state on a versioned
  MethodChannel, with native validation independent of screen validation.
- [Android owner](docs/android.md): private native service process, Keystore-wrapped
  identity, upstream WireGuard GoBackend Java API and a mobile DNS TUN adapter.
- [iOS owner](docs/ios.md): Keychain persistence, Packet Tunnel provider,
  WireGuardKit, pinned enrollment and platform split DNS.
- [Native DNS adapter](native/README.md): synthetic in-tunnel DNS address with
  UDP/TCP netstack interception and protected underlay public resolution.
- [Brand sources](assets/brand/SOURCE.md): the existing Meld logo and palette.

No private keys/device credentials enter Flutter or public status; enrollment
input crosses the native boundary once. Errors exposed to screens are sanitized.
Neither backend implements WireGuard cryptography: both embed upstream code.

## Verification boundary

Widget/model tests and native protocol tests use synthetic credentials. The Go
DNS tests run UDP and TCP packets through an in-memory TUN/netstack, including
private NXDOMAIN and failed-primary no-fallback checks; they do not alter host DNS
or routes. Unsigned Apple builds and Android packages are build verification,
not proof of background lifecycle or physical-device traffic. An isolated Android
emulator additionally passed real enrollment, WireGuard handshake, UDP/TCP private
DNS, private NXDOMAIN and public/system DNS against a disposable Linux primary.
See `../handoff.md` for commands actually run and remaining device checks.
Physical Android/iPhone testing, Wi-Fi/cellular handover, revocation, permission
revoke, process death, screen lock/Doze and production signing remain release gates.
