# Mobile owner bridge v1

MethodChannel `si.meld.meldnet/v1`. Only public snapshots return to Dart.
Native owner validates all inputs. Never send private keys/device credentials to
Dart, logs or errors. Enrollment key crosses only `join`. Generic sanitized
PlatformException codes/messages only; no raw remote responses. No UI persistence.

Methods: `snapshot` (no args); `join` with `{id,name,key,connect,autoConnect}`;
`connect` with `{id}`; `disconnect` (no args); `setAutoConnect` with `{id,enabled}`.
Each mutation returns a fresh snapshot with the same shape as `snapshot`.
Only one active membership; connect to another requires explicit prior disconnect.

Snapshot:
```
{version:1, platform:"android"|"ios",
 capabilities:{join:true,connect:true,autoConnect:false,multipleActive:false},
 activeId:null|"work", warning:null|"sanitized issue",
 profiles:[{id:"work",name:"phone",state:"disconnected"|"connecting"|"up"|"error"|"revoked",
 autoConnect:false,pending:false,address:null|"10.77.0.2",
 error:null|"sanitized issue",dnsState:"inactive"|"configured"|"ready"|"unavailable",
 peers:[{name:"desktop",hostname:"desktop.meldnet.internal",address:"10.77.0.3",
 evidence:"self"|"handshake"|"control"|"stale"|"unknown"|"disconnected"}]}]}
```

`up` means observed tunnel interface, never peer reachability. UI maps evidence
labels independently. No startup capability until the OS owner implements it.
Native app/extension owns saved identities independently of Flutter lifetime.

On iOS, `configured` means the primary advertised private DNS configuration; it
is not a successful lookup or DNS reachability claim. Android retains its existing
`ready` adapter state. Neither state proves that a peer is reachable.
