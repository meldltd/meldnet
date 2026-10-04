# Local API v1

HTTP/1.1 JSON over a Unix domain socket. No TCP listener. Requests must use Host
`meldnet` and omit Origin. Socket permissions and kernel peer credentials
authorize one local UID and root.
Mutation bodies use `Content-Type: application/json`; unknown fields, trailing
JSON, and bodies over 128 KiB are rejected.

| Method | Path | Meaning |
|---|---|---|
| GET | `/v1/status` | Initialization, backend, public node, observed tunnel/peers |
| PUT | `/v1/config` | Initialize or replace settings using a revision |
| POST | `/v1/up` | Start the tunnel, idempotently |
| POST | `/v1/down` | Stop the tunnel, idempotently |

`PUT /v1/config` takes `{ "revision": number, "settings": Settings }`. Use revision
0 only for initialization. Subsequent requests must use the last observed
revision; the daemon increments it on successful persistence. A conflict or an
edit while connected returns HTTP 409. Invalid requests return 400, unsupported
media type returns 415. Backend operation failures currently return 400 with a
safe diagnostic; clients must use the error body, not infer a failure category
from 400 alone. Success returns 200. Error bodies are `{ "error": "message" }`.

Settings fields:

- `name`: device label (1–32 ASCII letters/digits/dot/underscore/hyphen).
- `addresses`: 1–8 IPv4/IPv6 interface CIDRs, preserving host bits.
- `listen_port`: 0–65535; zero asks WireGuard to select one.
- `peers`: up to 128 peer objects, each containing `name`, `public_key`, optional
  `endpoint`, `allowed_ips` (1–64 network CIDRs), and `keepalive` seconds (0–65535).

The response to a configuration update includes `revision`, `public_key`, and
`settings`. Private keys are never exposed or accepted. WireGuard shell hooks,
DNS scripts, arbitrary configuration paths, and arbitrary directives are absent
from the contract.

Status `backend` is `wireguard` or `simulation`. A status response can return 200
with an `error` field: this means the daemon is reachable, but tunnel state could
not be fully observed. Treat the tunnel state as uncertain in that case.
`tunnel.up` describes interface presence, and each peer's optional
`last_handshake` is an RFC3339 timestamp. Counters describe actual WireGuard
bytes; simulation reports neither traffic nor handshakes.

Network mutations finish with a bounded 30-second timeout even if the requesting
frontend exits. Status observations are bounded to 5 seconds. Mutations are
serialized. There is no long-polling endpoint; the TUI polls every two seconds.

Example status request:

```sh
curl --unix-socket /var/run/meldnet/control.sock http://meldnet/v1/status
```

## Managed networks

The protected local API additionally exposes:

| Method | Path | Request / response |
|---|---|---|
| GET | `/v1/network` | `{role,pending,members,error?}`; public directory |
| POST | `/v1/network/invite` | Primary only; `{key}` returned once, expires in one hour |
| POST | `/v1/network/join` | `{name,key}`; enroll and configure a fresh client |
| DELETE | `/v1/network/members/{name}` | Primary only; revoke the device |

Member entries contain `name`, `hostname`, `public_key`, `ip`, and `last_seen`. Last seen is
control contact, not data-plane liveness. Never put enrollment keys in URLs or
command arguments. CLI `join --key-file FILE` and the masked TUI input use the
local socket request body. Manual configuration writes are refused for managed nodes.

The primary's separate Fiber HTTPS service exposes `POST /v1/register` with an
invite bearer credential and `{name,public_key,credential_hash}`, and
`GET /v1/network` with the persistent device bearer credential. These endpoints
return assigned IP, pool, primary public identity/endpoint, and the device directory.
They have no browser/CORS flow and require certificate pinning through the enrollment
key. Request bodies are bounded, connection/read/write times are bounded, and a
per-source rate limiter applies. Raw credentials and WireGuard private keys never
appear in server responses. Enrollment and TLS identities are separate from WireGuard.

### Private DNS status

`GET /v1/status` includes optional `dns`: `{domain,server,active,mode,error?}`.
DNS activation is distinct from tunnel health; a DNS conflict leaves the VPN
working by IP while `dns.active` is false and `dns.error` explains the problem.

`GET /v1/network` includes optional `dns`: `{domain,server}` and each member's
`hostname` (fully qualified, no trailing dot). The same DNS configuration travels
in the primary's registration/network response. The initial domain is fixed at
`meldnet.internal`; server is the primary's VPN IP. A missing DNS object preserves
compatibility with older primaries. No credentials are added to these responses.

## Independent network profiles

The original endpoints address `default`. Prefix any profile operation with
`/v1/networks/{id}` instead of `/v1`: for example `GET /v1/networks/work/status`,
`POST /v1/networks/work/up`, and `GET /v1/networks/work/network`.
Authentication and key isolation are unchanged.

| Method | Path | Contract |
|---|---|---|
| GET | `/v1/networks` | Array of `{id,auto_connect,status,network,warning?}` |
| POST | `/v1/networks/join` | `{id,name,key,auto_connect,connect}`; enroll a profile |
| PUT | `/v1/networks/{id}/autoconnect` | Required `{auto_connect:boolean}`; persist startup policy |

Profile JSON bodies are limited to 16 KiB, with unknown fields rejected. IDs use
1–31 lowercase letters/digits/hyphens, starting with a letter and ending with a
letter or digit. There are at most 16 profiles, including `default`. Join booleans
default to false when omitted. Legacy `/v1/network/join` retains connect-now and
auto-connect defaults for a new default enrollment. Existing profiles preserve
startup policy on enrollment retries. Changing startup policy never connects or
disconnects the current tunnel. Missing profiles return 404; address-range conflicts
return 409 with the network names and ranges. A join may enroll successfully and
then return 409 because its connect-now request conflicts; refresh the list before
retrying enrollment. No private keys are returned. Scoped directory responses
contain hostnames qualified by the local profile ID.
