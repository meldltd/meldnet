# Optional background service installation

Build with `make build`; no WireGuard package installation is needed. Linux needs
its OS TUN device and macOS uses built-in utun. Stop any
foreground production daemon before starting a service. Run these commands from
the repository as the normal user who should control the VPN; `id -u` must
identify that user, not a root shell.

Install the binaries on either platform:

```sh
sudo mkdir -p /usr/local/libexec /usr/local/bin
sudo install -m 0755 bin/meldnetd /usr/local/libexec/meldnetd
sudo install -m 0755 bin/meldnet /usr/local/bin/meldnet
```

Use an administrator-owned installation directory; do not let other users
replace the privileged daemon binary or its parents. A root service controls
routing, so granting socket access is an administrative decision.

## Linux / systemd

```sh
unit_file="$(mktemp)"
sed "s/@UID@/$(id -u)/g" packaging/meldnetd.service.in > "$unit_file"
sudo install -m 0644 "$unit_file" /etc/systemd/system/meldnetd.service
rm "$unit_file"
sudo systemctl daemon-reload
sudo systemctl enable --now meldnetd
meldnet
```

Diagnostics: `sudo journalctl -u meldnetd`. Stop and disable with
`sudo systemctl disable --now meldnetd`. To remove the unit, remove
`/etc/systemd/system/meldnetd.service` and run `sudo systemctl daemon-reload`.

## macOS / launchd

```sh
plist_file="$(mktemp)"
sed "s/@UID@/$(id -u)/g" packaging/si.meldnet.daemon.plist.in > "$plist_file"
sudo install -m 0644 "$plist_file" /Library/LaunchDaemons/si.meldnet.daemon.plist
rm "$plist_file"
sudo chown root:wheel /Library/LaunchDaemons/si.meldnet.daemon.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/si.meldnet.daemon.plist
meldnet
```

Diagnostics: `/var/log/meldnetd.log` and
`sudo launchctl print system/si.meldnet.daemon`. Stop with
`sudo launchctl bootout system/si.meldnet.daemon`. To uninstall the service,
remove `/Library/LaunchDaemons/si.meldnet.daemon.plist` after bootout.

Both templates start the daemon after boot. Managed nodes reconnect automatically;
manual nodes remain disconnected until the user connects. Service shutdown or a
daemon crash closes the embedded tunnel. Startup recovers endpoint routes.
The macOS template's KeepAlive restarts an unexpectedly exiting daemon; use
bootout to stop it deliberately. These are development templates, not signed
installers. Provisioning and upgrades are manual in this milestone.

Private identity data remains when uninstalling. Back it up securely if needed;
deleting it and initializing again changes the node's public key. Never delete
runtime state while a tunnel is up or cleanup has failed.

## Managed primary/client startup

Initialize a primary once using the README primary flags before installing its
service, then stop that foreground daemon. The ordinary service command loads
persisted primary options from the same default state directory. Clients can join
through the local TUI/API after their service starts. Managed nodes automatically
reconnect after service restart; manual nodes still require `meldnet up`.

Allow the primary's configured HTTPS TCP and WireGuard UDP ports through existing
firewalls. These templates do not provision firewall rules, DNS, or certificates
from an external authority; enrollment pins the generated primary certificate.

## macOS menu bar installation

`./scripts/install-macos.sh` builds the native AppKit app and Go binaries. Run it
as the intended desktop user, not from a root shell. It requests sudo for the
root-owned daemon binary and LaunchDaemon, enabling the daemon at the **next
boot** without stopping or replacing the current running process. It uses the
default daemon state and socket paths and does not enroll a new identity. Existing
managed configuration reconnects after reboot; manual configuration still needs
an explicit `meldnet up`. Do not run it against custom paths without adapting
the launchd arguments first.

The unprivileged half copies the app to `~/Applications/Meldnet.app`, writes
`~/Library/LaunchAgents/si.meldnet.menubar.plist`, and starts that LaunchAgent.
`--menu-only` performs only this half and needs no administrator password. Re-run
the installer to update the app. The LaunchAgent starts at login, with no KeepAlive
so choosing Quit is respected until the next login. The daemon's KeepAlive is
independent of the app. No private configuration is read by the app.

The menu offers **Open TUI**, **Enable VPN / Disable VPN** and **Join Network…**.
Open TUI launches the bundled CLI in Apple's Terminal under the current user,
passing the menu app's control socket (including a custom `--socket` path). It
requires no administrator or Terminal automation permission. The menu item is
available even when the daemon is unavailable; it never starts the daemon.
The private temporary command document is removed when Terminal executes it.
Launch errors are shown in the app. Connection
changes use the local daemon API and refresh observed status after completion or
failure. Managed disconnect pauses until explicit enable or daemon restart.
Joining uses a device name and masked enrollment key, sent only in the local API
request body; it does not save the key in frontend preferences, files or logs.
A fresh daemon can join, and pending enrollment can be retried. Already joined
clients, primaries and initialized manual nodes are preserved rather than reset.
The daemon independently validates all mutations. Controls are disabled while a
request is pending; failures are shown without echoing enrollment credentials.

Rows copy the exact directory hostname, not a name synthesized by the UI. Entries
without assigned hostnames cannot be copied. Status means:

- **Recent handshake:** the local daemon reports a WireGuard handshake within
  three minutes. This is historical evidence, not an active reachability probe.
- **Active / Inactive · control:** directory contact was within / beyond fifteen
  seconds. Contact with the primary does not prove VPN traffic reaches the peer.
- **Status unknown:** directory synchronization failed and no recent local
  handshake is available. Disconnection and simulation are labeled explicitly.
- **This Mac:** the directory public key matches the local identity.

To stop the menu app from opening at login, without affecting the daemon:

```sh
launchctl bootout "gui/$(id -u)/si.meldnet.menubar"
rm "$HOME/Library/LaunchAgents/si.meldnet.menubar.plist"
```

You can then move `~/Applications/Meldnet.app` to Trash. To remove daemon startup,
follow the separate LaunchDaemon removal instructions above. If it has not yet
been loaded since installation, skip bootout and remove its plist. VPN identity
and recovery state must be retained.

The app is locally ad-hoc signed, not notarized for distribution. Building it
requires Apple's Swift/AppKit toolchain; the Go daemon and CLI still build with
CGO disabled. `make macos-test` checks the native client against temporary Unix
sockets with synthetic public data; it never connects a VPN or edits host DNS.

## macOS installer package

`make macos-installer` builds a universal `.pkg` using Apple's `pkgbuild` and
`productbuild`. Both Go binaries use CGO-disabled builds for amd64 and arm64;
Swift/AppKit is also compiled for both architectures. Both the package and
standalone menu app require macOS 13 or newer, matching the bundled Go 1.27
terminal client's binary deployment target.

The package contains:

| Installed path | Purpose / permissions |
| --- | --- |
| `/Applications/Meldnet.app` | Root-owned menu app; runs as the desktop user |
| `/Applications/Meldnet.app/Contents/Resources/meldnet` | Bundled CLI, executable mode 0755 |
| `/Library/PrivilegedHelperTools/si.meldnet.daemon` | Root-owned executable, mode 0755; launchd runs it as root |
| `/Library/LaunchDaemons/si.meldnet.daemon.plist` | Generated root:wheel, mode 0644; boot service |
| `/Library/LaunchAgents/si.meldnet.menubar.pkg.plist` | Generated root:wheel, mode 0644; authorized account's login app |
| `/Library/Logs/Meldnet/daemon.log` | Root-only log directory/file, modes 0700/0600 |

Installer handles administrator authorization; neither the app nor a custom
password dialog collects administrator credentials. The package is restricted to
the running startup disk. On a first install a desktop user must be logged in;
that console UID receives local API access. The account entering an administrator
password may be different from the authorized desktop user. Upgrades preserve
`--allow-uid` from an existing recognized service instead of transferring access
to another logged-in account. Unknown commands or custom daemon arguments are
rejected for manual migration. Headless first installs are not supported.

The system login agent passes `--only-uid` so the app immediately exits in other
accounts. Other users do not gain access to the daemon's 0600 socket or its
root-owned private state. The daemon binary lives in a protected Library directory,
not a user-writable app bundle or Homebrew directory. Installer scripts reject
unsafe ownership, writable privileged destinations, symbolic links and hard-linked
files. The payload carries root ownership regardless of the account building it.
The app cannot relocate into an old copy in a user's home.

No restart is required. Shut down any previous foreground daemon yourself before
installing. The installer enables the boot service and bootstraps it immediately
if its launchd job is not already loaded. It never kills, unloads, or restarts an
existing daemon job; on upgrades, stop/unload the old job yourself if you want the
new binary to take effect immediately. Startup failures from launchctl fail the
installation rather than being ignored. Successful job loading is not proof of
VPN connectivity; check the app or root-only daemon log for runtime errors.

The menu LaunchAgent is enabled and loaded immediately if the authorized user's
GUI session exists; otherwise it starts on their next login. Already loaded jobs
are preserved. If a foreground daemon was left running, the daemon's existing
ownership locks prevent a second instance from managing the tunnel. Stop the old
process yourself; launchd's KeepAlive retries the installed service. Existing default
state under `/Library/Application Support/Meldnet` is never bundled, overwritten,
read, or deleted by the installer. Managed nodes reconnect at boot; manual nodes
still need `meldnet up`. Permission/setup failures fail the installation; generated
launchd plists are published atomically.

A legacy `~/Applications/Meldnet.app` is left intact. The system app takes
precedence if the old login agent also starts its copy. Remove the old per-user
LaunchAgent using the earlier instructions when convenient. No privileged
installer script writes into a user's home.

For distribution signing, use identities already present in the build keychain:

```sh
CODE_SIGN_IDENTITY='Developer ID Application: Your Name (TEAMID)' \
INSTALLER_SIGN_IDENTITY='Developer ID Installer: Your Name (TEAMID)' \
make macos-installer VERSION=0.2.0
```

Both identities must be supplied together. This enables hardened-runtime,
timestamped code signatures and an Installer signature. Notarization/stapling is
a separate release step requiring an Apple developer account; this target does
not claim to notarize the package. Without identities the package remains an
unsigned local-development installer with ad-hoc signed code. Do not treat it as
a notarized public release or disable system protections to distribute it.

`make macos-installer-test` expands the archive, inspects ownership/modes,
architectures, signatures and startup policy, then runs installer scripts against
a temporary filesystem with mocked account/privilege/launchctl operations. These
checks do not prove native privileged installation or live launchd activation; test those
on a disposable Mac/VM before distributing broadly.

To remove the packaged installation, quit the menu app, stop the service with
`sudo launchctl bootout system/si.meldnet.daemon`, and verify cleanup succeeds.
Unload the login agent with `launchctl bootout gui/$(id -u)/si.meldnet.menubar.pkg`.
Then remove the two launchd plists, the app, and the privileged executable at the
paths above with administrator privileges. If jobs have not loaded yet, skip
bootout for those jobs. Preserve private identity/recovery state and inspect any
cleanup errors; do not remove resolver journals as part of app uninstallation.
