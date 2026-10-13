# Android native owner

The Android app supports Android 11/API 30 and newer on arm64-v8a and x86_64.
Compile/target SDK 36 and Android NDK 28.2.13676358 are pinned in Gradle.
Flutter screens talk to an application-private Messenger Binder in `:vpn`.
`OwnerService` owns enrollment, encrypted storage, control reconciliation and
WireGuard; it never starts a Flutter engine. The upstream `GoBackend.VpnService`
in that same process is the only OS VPN/TUN service. Closing Flutter unbinds the
screen without stopping a started active owner. Process death destroys the tunnel;
no persisted flag is presented as a live connection.

The official `com.wireguard.android:tunnel:1.0.20260102` Java classes are extracted
from its pinned AAR. Its stock native libraries are deliberately not packaged.
The only `libwg-go.so` files come from `../native/build/jniLibs` and contain the
pinned upstream WireGuard implementation plus Meldnet's DNS interception adapter.
The owner checks `GoBackend.version == meldnet-dns-v1` before activation. No shell,
external WireGuard utility, root, second TUN or presentation-owned key is used.

## Build and verification

Run the repository's mobile native build first, then from `mobile/android` with
Java 21 and an Android SDK configured in local.properties:

```
./gradlew :app:assembleDebug :app:testDebugUnitTest :app:lintDebug
ANDROID_SERIAL=<isolated-test-emulator> ./gradlew :app:connectedDebugAndroidTest
```

Debug APK output is `mobile/build/app/outputs/apk/debug/app-debug.apk`.
Release builds intentionally have no debug signing configuration; distributors
must supply their own signing. Do not commit keystores or local.properties.
Builds require network access for the pinned toolchain and Maven dependencies.

Unit tests cover typed native commands, enrollment origin/secret bounds, assigned
network/pool/endpoint/DNS validation, saved-device-credential recovery before
registration, revocation, and absence of private identity/device credentials in
registration payloads. Instrumentation checks actual JNI loading/configuration,
real Android Keystore AES-GCM persistence/tamper detection in isolated test
storage, and the native Binder snapshot without a Flutter engine. None of those
tests grants VPN consent, establishes a tunnel or changes host routing/DNS.

## Security and lifecycle

Secrets live in an owner-only no-backup directory encrypted with an Android
Keystore AES-GCM wrapping key; writes use AtomicFile. Cloud and device-transfer
backups are explicitly excluded. Plaintext private keys are briefly required in
the owner for the established WireGuard implementation. Public snapshots are
constructed from an allowlist and contain no private/backend configuration.
A corrupted/missing wrapping key fails closed and preserves encrypted state.

The owner durably writes each new identity and device credential before making
any registration request. It first tries that credential against `/v1/network`,
registering with the invite only after 401 for an unregistered identity. The
invite is cleared after a validated durable assignment. A registered identity's
401 stops its tunnel; an unreachable primary keeps an existing valid tunnel.
Failed enrollment can be retried with the same profile ID and name, preserving
the identity. There is no profile deletion/reset command in this milestone.

The native Go TLS transport supports the primary's Ed25519 self-signed certificate
without relying on Android system-TLS algorithm support. HTTPS pins the exact
enrollment leaf certificate and checks its validity dates,
requires TLS 1.3, disables proxies and redirects, binds to a captured non-VPN
Android Network and bounds bodies to 128 KiB. Each native HTTPS request has an 18-second total deadline. Android underlay DNS
resolution uses a cancellable five-second deadline.
Remote errors and enrollment input never enter logs or UI error messages.

Only one membership may be active; selecting another requires disconnect first.
Control reconciliation uses bounded 15–300 second backoff. Network changes
immediately revalidate local DNS policy before remote requests/backoff and
reapply a valid cached tunnel configuration when the underlay changes. The
foreground notification keeps the owner independent of the screen; always-on,
boot auto-connect and saved startup preference remain deliberately unsupported.
The app requires Android's ordinary VPN consent. Foreground service special-use
review and production signing/distribution remain release tasks.

## Private DNS

The owner adds a fixed synthetic DNS address/route `198.18.0.53/32` internally
after validating the primary's private pool. The native TUN adapter intercepts
UDP and TCP DNS before WireGuard encryption. Private-zone/reverse queries bind explicitly to the UID-owned VPN Network and go to
the primary over the VPN; other queries use captured underlay resolver sockets
bound to their Android Network handle. Private misses never fall through to a
public resolver. The app does not modify host resolver files.

Connection requires a valid DNS authority, underlay DNS and DNS-aware native
backend. Strict system Private DNS is rejected because it can bypass the VPN's
plaintext DNS selection; the connection stops if that configuration appears
while active. Applications using their own encrypted DNS remain outside the
system DNS adapter's control. Lockdown, another VPN replacing Meldnet, physical
Wi-Fi/cellular handover and OEM background policies need device acceptance.

Interface state is separate from reachability. Only the primary's recent actual
WireGuard handshake yields handshake evidence; member timestamps are control
activity, not direct-mesh connectivity. The primary remains a trusted relay.

## Isolated live acceptance exercised in this checkpoint

`LivePrimaryTest.encryptedPrimaryAndPrivateDNS` is explicitly opt-in: ordinary
instrumentation skips it. It requires a disposable emulator with VPN consent and
an app-private `files/test-invite` containing a synthetic enrollment key, plus
instrumentation argument `inviteFile=test-invite`. Never put the key itself in
arguments or logs. The fixture is deleted after enrollment; the test disconnects
in `finally`. Run against an isolated primary with endpoint UDP port mapped to
that same port (the primary's endpoint port is also its listen port).

The Android35 arm64/16KiB-page emulator passed real enrollment, public snapshot,
WireGuard handshake, UDP/TCP private A and NXDOMAIN, public DNS, and Android system
private/public resolution. It waits for Android to select the VPN before opening
application sockets. This proves encrypted emulator traffic; it does not replace
physical-device background, handover, permission-revoke or process-death tests.
