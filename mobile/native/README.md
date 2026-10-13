# Android mobile WireGuard/DNS adapter

This separate Go module links upstream wireguard-go and its gVisor netstack;
it does not change desktop backends or implement cryptography. GoBackend's Java
API comes from the pinned official Android tunnel artifact, but its stock JNI
library is excluded. `scripts/build-mobile-native.py` produces the sole JNI
libraries under `build/jniLibs/` for arm64-v8a/x86_64, with 16 KiB ELF page alignment.
The Android owner checks `wgVersion` for `meldnet-dns-v1` before connecting.

`NativeDns.configure` accepts a validated primary/pool/underlay DNS policy and
Android underlay and VPN Network handles inside the native owner only. GoBackend creates the one
VPN TUN, routes the managed pool plus synthetic `198.18.0.53/32`, and advertises
that synthetic DNS address. The TUN wrapper intercepts packets addressed there
before WireGuard; the upstream netstack terminates UDP/TCP and returns DNS replies
to the TUN. Other packets pass unchanged to upstream WireGuard encryption.

The proxy sends `meldnet.internal` and covering pool reverse zones exclusively
to the primary VPN address with sockets bound to that exact VPN Network;
private requests fail closed before the VPN handle is available. NXDOMAIN, REFUSED and primary failures never fall
through to public servers. Other names go to captured underlay DNS servers using
`android_setsocknetwork`, avoiding recursion into the VPN. UDP truncation retries
TCP. Queries, response sizes, concurrency, socket deadlines and TCP lifetimes are
bounded. Android rejects unsupported strict Private DNS/invalid local policy;
platform Private DNS and handover still require device packet-capture acceptance.
Reverse zones conservatively cover whole octets, so some adjacent private PTRs
may return authoritative NXDOMAIN rather than use the underlay.

The JNI function signatures/C shim are derived from the Apache-2.0 upstream
`wireguard-android/tunnel/tools/libwg-go/jni.c`, retaining its copyright header.
The customized Go entry point deliberately omits raw UAPI listeners, logging,
private-key-bearing config returns and process signal stack dumps. The module's
`go.mod`/`go.sum` pin all Go dependencies. Upstream WireGuard and gVisor licenses
remain applicable; distribute their notices with production builds.

```
cd mobile/native
go vet ./...
go test -race ./...
```

These tests use fake/in-memory TUNs and synthetic DNS; they never modify host
networking. Android owner integration and real encrypted device traffic require
separate native acceptance. The isolated emulator test is recorded in the handoff.

## Pinned control transport

The `control` package uses Go TLS 1.3 for the primary's Ed25519 certificate,
which the tested Android and Apple system HTTPS providers could not negotiate.
It validates the exact enrollment leaf fingerprint and validity dates, disables
proxies and redirects, bounds headers/bodies, and enforces an 18-second request
deadline. Android resolves through the selected underlay with a cancellable
five-second bound and binds control sockets to its Network handle. This interface
is called only inside the native owner; credentials never enter Flutter status.
