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

Both templates start the daemon after boot but leave a cleanly stopped tunnel
disconnected until the user connects. Service shutdown or a daemon crash closes
the embedded tunnel. Startup recovers endpoint routes but does not reconnect.
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
