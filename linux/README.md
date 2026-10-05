# Linux desktop interface

`meldnet_gui.py` is an unprivileged native GTK 3 frontend to the existing local API.
It never reads daemon state, starts a daemon, changes routing or invokes sudo.
It shows per-network connect/disconnect, independent startup preferences, masked
join, conflicts, DNS errors and public peers; click a peer to copy its hostname.
Quitting keeps the VPN running. Control activity and interface-up status do not
prove peer reachability. Polling and mutations run on a serial worker with bounded
I/O; failures clear stale snapshots and mutations always refresh observed state.

Install Python 3, PyGObject, GTK 3 and optionally Ayatana AppIndicator from your
Linux distribution. On Debian/Ubuntu these are `python3`, `python3-gi`,
`gir1.2-gtk-3.0`, `gir1.2-ayatanaappindicator3-0.1`. The Go daemon stays CGO-free;
these presentation dependencies are only for the optional desktop app.

Launch `python3 linux/meldnet_gui.py` after an administrator has configured and
started the daemon with your `--allow-uid`. An AppIndicator menu provides the
macOS-style per-network controls; the window stays available on GNOME/Wayland and
desktops that do not display tray indicators. Closing the window hides it when
an indicator is available; Quit app ends the frontend. Without AppIndicator,
closing the window quits the frontend. Tray visibility depends on desktop support
(e.g. GNOME's AppIndicator extension). No terminal launcher is included.

For a custom/simulation endpoint: `python3 linux/meldnet_gui.py --socket PATH`.
`--check` prints public network/peer evidence without requiring GTK.
`make linux-gui-test` runs the portable model tests and, on Linux, a fake Unix API
transport test. `scripts/smoke-linux-gui.py` additionally exercises GTK under Xvfb
using an in-memory API fake and writes a screenshot. Neither test uses host DNS.

`make linux-gui` builds `bin/Meldnet-linux-gui.tar.gz`. The archive contains the
GUI, desktop entry, optional systemd unit template and explicit administrator
installer. It does not include a daemon binary. Copy the appropriate CGO-free
`bin/linux-ARCH/meldnetd` to `/usr/local/sbin/meldnetd` and CLI to
`/usr/local/bin/meldnet` separately. Run the installer with the allowed numeric
UID; for an extracted archive use `sh install-linux.sh UID .`. Review and
explicitly enable/start `meldnetd.service` afterward. The installer does not
activate it, change a running service, or overwrite private daemon state.
GUI login startup is optional: place the desktop entry in your own
`~/.config/autostart`. Do not run the desktop app as root.
