# iOS and Android implementation assessment

Assessed 2026-10-05. These are implementation plans, not shipped mobile apps.
The desktop daemon cannot simply be packaged as a persistent root process on
stock phones. Retain the existing enrollment protocol and trusted-primary relay,
but give tunnel lifetime, secrets and OS integration to the platform's VPN
extension/service. Closing a screen must leave that owner running. Native VPN
permission prompts remain necessary; UI code cannot grant them itself.

## Shared product and protocol

Start with managed clients only. The existing primary accepts HTTPS
`POST /v1/register` with `{name,public_key,credential_hash}` and an invite Bearer
credential, followed by authenticated `GET /v1/network` using a device credential.
A `meldnet1.` enrollment key contains origin, certificate SHA-256 pin and secret.
Use the same maximum lengths, one-use enrollment, idempotent registration,
client-generated identity, persistent credential-before-registration ordering,
certificate date checking, TLS 1.3, no redirects and no proxy behavior as desktop.
The primary must never receive a WireGuard private key. Validate configuration
and overlap again inside the extension/service, independently of the screen.
A lost registration response must resume by trying the saved device credential;
401 after successful enrollment means revocation and stops that network.

Existing members, allocated /32, primary public key, endpoint, pool and DNS
response are sufficient for a first client. Keep the private pool as the primary
peer's AllowedIPs and the existing keepalive policy. This remains encryption to
a trusted relay that can see client transit traffic; mobile does not create a
direct or end-to-end encrypted client mesh.

Control reconciliation currently polls every three seconds. Mobile needs bounded
retry/backoff, wake/reconnect reconciliation and underlay changes rather than a
promise of uninterrupted background polling. A primary outage keeps an existing
valid tunnel until revocation or explicit disconnect is known. Display interface,
recent handshake and control activity separately, with stale/offline evidence.
No key/credential strings belong in UI snapshots, clipboard, logs, crash reports,
analytics, command arguments or unencrypted exports.

One mobile OS VPN interface should be the first milestone. Persist multiple
memberships but activate one at a time; switch explicitly and preserve independent
preferences. Do not promise desktop's simultaneous independent TUNs. Supporting
several Meldnet networks in one OS tunnel later requires a deliberate adapter
that merges peers, non-overlapping routes, identity contexts and private DNS,
plus tests for partial disconnect and revocation. Separate device identities per
membership must not be accidentally collapsed into one shared WireGuard key.

## iOS

Build a SwiftUI app and a signed Network Extension target whose
`NEPacketTunnelProvider` owns the tunnel. `NETunnelProviderManager` stores the
public system VPN configuration and requests user approval. App-to-provider
messages expose a narrow, versioned command/status envelope analogous to local
v1; never expose raw backend configuration. Enrollment input can cross the
same application's authorized IPC once, then be cleared by the screen.

Use upstream WireGuardKit and its wireguard-go bridge inside the provider, with
`NEPacketTunnelNetworkSettings` for addresses/routes and `packetFlow` for packets.
The provider runs independently of UI lifetime. Do not include the desktop
Unix-socket server, root daemon, PF_ROUTE recovery code, launchd jobs or menu app.
A useful future refactor separates enrollment validation/state transitions from
`control.Manager` (currently coupled to `service.Service`) behind a platform
persistence/transport interface; do not import all desktop OS packages into the
mobile bridge. Alternatively, implement the small HTTPS control client natively
and verify it against the same protocol fixtures and real primary.

Store private identities and device credentials in Keychain using an explicitly
scoped access group accessible to the extension. Persist only public profile
references in `providerConfiguration`. Choose Keychain accessibility deliberately:
after-first-unlock is needed for background reconnect; before-first-unlock and
force-quit behavior must be tested rather than claimed. An App Group can store
public preferences but must not become a plaintext private-key export. The
provider should resolve/refresh the underlay endpoint, react to path changes,
and reapply routes safely. OS shutdown destroys its userspace tunnel.

DNS uses `NEDNSSettings.matchDomains` for the private zone and necessary reverse
zones, not writes to `/etc/resolver`. Prevent private NXDOMAIN from falling
through to system/public resolvers. For one membership, direct private queries
to its primary VPN address; for profile-qualified names compatible with desktop,
use a provider-owned DNS authority/proxy reached through the tunnel packet flow,
with exactly the desktop alias/ambiguity behavior. Binding a normal host loopback
server alone does not establish that iOS resolver traffic reaches it. Route the
chosen VPN DNS address and test UDP, TCP, PTR, misses, disconnect and underlay
changes. Do not enable all-domain DNS by accident.

Required delivery prerequisites: Apple Developer signing/provisioning, Network
Extension capability with packet-tunnel-provider entitlement, explicit access
and App Group entitlements where used, real iPhone testing and App Store review.
WireGuardKit's bridge uses native linkage: desktop's CGO-disabled release policy
continues for desktop, but an iOS framework requires its own documented build
and signing pipeline. No iOS target or entitled app is created in this change.

## Android

Build a Kotlin app plus an Android `VpnService`, protected by
`android.permission.BIND_VPN_SERVICE` and advertised by the
`android.net.VpnService` intent action. Call `VpnService.prepare()` and complete
the system consent flow, then let the service own persistence, reconnection and
backend state. Use a narrow app-private Binder API for public state/typed
mutations; do not export enrollment or key-bearing control components.

Embed upstream `com.wireguard.android:tunnel` GoBackend, whose official userspace
backend already handles the VPN service/TUN and socket protection. If supplying a
custom service/Go bridge, use `VpnService.Builder` to configure addresses,
AllowedIPs routes and DNS, pass the established file descriptor into the backend,
and call `protect()` for every backend/control socket that could otherwise
re-enter VPN routes. Do not run `wg`, `su`, shell scripts or require root. Ensure
the service integrated by the upstream backend remains the sole VPN owner; do
not create two competing VpnService instances.

Android permits one active VPN service per user/work profile. Starting another
VPN replaces the existing one. The first Meldnet version should activate one
membership at a time. Foreground service notification and appropriate current
SDK service declarations/permissions are required for background lifetime; the
platform VPN exemption and current target SDK policies need confirmation during
implementation. Handle `onRevoke`, process death, boot/always-on and network
callbacks, and close descriptors/sockets promptly. Always-on/lockdown are OS
policies, not equivalent to Meldnet's saved auto-connect preference.

Use app-private storage with Keystore-protected encryption for WireGuard keys
and device credentials. A WireGuard key is needed as backend key bytes: Android
Keystore cannot substitute its opaque non-exportable signing key for that key.
Use a Keystore wrapping key, decrypt only in the VPN owner, and disable or
carefully scope automatic backups. React to Wi-Fi/cellular handover, IPv6-only
underlay, Doze and battery restrictions. Keep retries bounded and reconcile
revocation after resuming; do not promise a permanent three-second heartbeat.

`VpnService.Builder.addDnsServer()` does not offer Apple's per-domain matching.
A direct private-only DNS server would break public resolution if used globally.
Use an in-tunnel DNS proxy address under Meldnet ownership that sends only private
zones/reverse pools to the VPN authority and forwards other queries to captured
underlay DNS through protected/bound sockets. Never send private misses to those
upstreams. Coordinate endpoint/control-socket protection and underlay DNS refresh
on network change; test TCP fallback, large responses, Android Private DNS,
lockdown and another VPN replacing Meldnet. This is a separate mobile adapter,
not host resolver-file journaling.

## Delivery order and acceptance

1. Introduce platform-neutral managed-client protocol fixtures (synthetic secrets),
   pinned TLS enrollment, credential recovery and revocation tests. Decide whether
   shared Go control logic or native clients will own those transitions.
2. Deliver one Android managed client against the existing Linux primary using
   the upstream embedded backend; exercise service/background and DNS behavior
   on an emulator and physical device. Then implement the equivalent iOS provider.
3. Add the native screens: memberships, masked enrollment, observed connect state,
   peers/hostnames, separate startup preference and explicit profile switch.
4. Verify real encrypted client-to-primary and relayed client traffic, private DNS
   no-leak behavior, invalid/expired certificate pins, permission denial/revoke,
   process death, restart without UI, Wi-Fi/cellular handover, screen lock/Doze,
   stale control status and identity continuity. OS indicators alone are not proof.
5. Only after those gates consider concurrent memberships, always-on/on-demand,
   battery optimization and store distribution. No mobile primary-hosting claim.

## Authoritative references

- [WireGuard embedding guidance](https://www.wireguard.com/embedding/) names
  WireGuardKit for Apple and the Android tunnel library for embedding.
- [Apple NEPacketTunnelProvider](https://developer.apple.com/documentation/networkextension/nepackettunnelprovider)
  defines the tunnel provider lifecycle.
- [Apple packet tunnel settings](https://developer.apple.com/documentation/networkextension/nepackettunnelnetworksettings)
  and [NEDNSSettings](https://developer.apple.com/documentation/networkextension/nednssettings)
  define system routing and DNS configuration.
- [Android VPN guide](https://developer.android.com/develop/connectivity/vpn)
  documents consent, service ownership, socket protection, foreground notification,
  one-service limit, revocation and always-on behavior.
- [Android VpnService API](https://developer.android.com/reference/android/net/VpnService)
  defines TUN creation/protection and lifecycle hooks.
