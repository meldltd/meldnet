# Windows desktop, tray and daemon (experimental)

Windows 10/11 amd64 and arm64 binaries are included in `make cross`, with CGO
turned off. `make windows-package` builds architecture-specific ZIPs containing
the daemon, native desktop/tray app, CLI/TUI, official signed Wintun 0.14.1 DLL, its redistribution license
and a PowerShell installer. The build checks the archive's published SHA-256;
the installer also checks the DLL's Authenticode signature. Meldnet binaries are
currently unsigned development builds. The GUI uses built-in Win32 controls and
requires no WebView, .NET, Python, or additional GUI runtime.

Run the extracted `install.ps1 -AllowedSID S-1-...` in Windows PowerShell 5.1
with administrator authorization. Pass the SID of the *individual user* who will
control the VPN (that user can obtain it from WindowsIdentity.GetCurrent().User).
The script installs under Program Files and creates/starts an automatic
LocalSystem service. It refuses an existing service; upgrades need an explicit
stop, preservation of its SID/service arguments, binary replacement and restart.
Never delete state to upgrade. Installation is not performed by build or tests.

The authorized user opens `meldnet-gui.exe` for the desktop window and tray icon.
Use `meldnet-gui.exe --tray` to start hidden in the notification area (Windows
may place the icon in its overflow menu). `--socket` selects an alternate Meldnet
pipe for a separately started development daemon. Closing the window hides it;
click the tray icon or choose **Open Meldnet** to restore it. Right-click offers
connect/disconnect for the selected network and **Quit app**. If a tray icon
cannot be created, the window stays available; closing it quits only the app.
Explorer restarts trigger tray re-registration. Launching the GUI never installs,
starts, stops, or elevates the daemon. GUI launch at login is not installed;
users may add a shortcut with `--tray` to their own Startup folder if desired.

The window includes a network selector, independent connect/disconnect and
connect-at-service-startup controls, public peer hostnames with copy support,
and a masked enrollment form with separate connect-now/startup choices. Secret
input is cleared on submission and window close and is never persisted or logged.
A failed join may already have saved enrollment: review the refreshed profiles
before retrying. Requests run off the UI thread, mutations refresh observed state,
and API failures discard old snapshots. Error responses are never echoed in the
GUI. Labels distinguish simulation, interface state, recent handshakes, and
control-directory activity; interface state alone does not prove reachability.
The interface uses native keyboard-focusable controls and system-DPI scaling;
per-monitor DPI, screen-reader behavior and visual polish need native acceptance.

`make windows-gui` builds only the amd64/arm64 graphical executables, without a
console window; `make cross` and `make windows-package` include them too. The
installer copies the app alongside the daemon. Open it from Program Files/Meldnet
as the authorized user, separately from the elevated installer. No app shortcuts
or automatic GUI launch are installed.

The authorized user can also run `meldnet.exe networks`, `meldnet.exe tui`, or other
existing CLI commands. Enrollment is read from stdin or `--key-file`, never from
arguments. The GUI/TUI is independent of the service. The service supports SCM
stop/shutdown and performs the same lifecycle cleanup as Unix.

Local HTTP v1 uses `\\.\pipe\Meldnet\control`: a protected DACL allows only
LocalSystem and the selected user, rejects remote pipe clients and prevents users
creating another instance. Clients check the server's privileged owner before
sending enrollment. `--allow-uid` applies only to Unix; Windows requires
`--allow-sid`. For foreground development use an elevated terminal, the same SID,
and an explicit private data directory. Simulations also require a separate
pipe, e.g. `\\.\pipe\Meldnet\demo`, and never report real traffic.

Private directories/files use protected owner-only DACLs, not Unix mode bits;
reparse paths and foreign owners are rejected. Atomic replacement uses the
Windows write-through API. An exclusive file handle guards each state directory;
a ProgramData lock guards networking across daemon instances. Production service
state is in ProgramData/Meldnet/state and belongs to LocalSystem. Never copy Unix
identity files into an ordinary shared Windows directory.

Embedded wireguard-go uses the bundled Wintun driver. IP Helper APIs configure
addresses and routes; no `wg`, PowerShell, netsh, shell or child process runs in
the daemon. Endpoint journals include interface LUID, name, next hop and owned
route metric/protocol so recovery preserves replacements. Host-sized interface
addresses avoid implicit whole-subnet routes. Wintun's adapter/session is owned
by the process: a userspace VPN cannot survive daemon death.

Private DNS uses local Windows NRPT rules for meldnet.internal and configured
reverse zones, pointing only at the loopback aggregate. A private intent journal
precedes registry writes; recovery removes only exact owned values, including
partial installation, and retains errors/journals for external edits or cleanup
failure. Existing foreign local or Group Policy NRPT rules block activation
instead of being overwritten. Registry notifications publish the changes and
the native DNS API clears cached answers; enterprise policy interactions and
native resolver behavior still require Windows validation. No global DNS server
setting is replaced. If DNS activation fails, the API reports it while the VPN
can remain up; do not assume private names resolve until that error clears.

Native desktop acceptance checklist (disposable Windows 10/11 machine):

- Start the app with the service stopped: responsive window, no stale peers,
  actionable unavailable state, and no automatic service start.
- Test authorized/unauthorized accounts and both architecture packages. Join
  two test networks, switch selection during requests, toggle startup separately
  from connection state, and verify timeout/overlap/cleanup errors refresh state.
- Check masked/cleared enrollment, keyboard navigation, clipboard, screen reader,
  100–200% scaling, tray overflow, Explorer restart, `--tray`, and window reopen.
- Close and quit the app while a test tunnel runs; verify the independent service
  and encrypted traffic continue. Check that simulation is explicitly labeled.

Cross-compilation verifies buildability only. The desktop has not been visually
or interactively tested on Windows in this development session. Native Windows named-pipe access,
DACLs, SCM installation, Wintun lifetime, encrypted IPv4/IPv6, crash cleanup,
NRPT policy activation and external-edit preservation must pass in a disposable
Windows VM before production support is claimed. This development machine has
not run a Windows tunnel or installer. Mobile platforms are assessed in
`docs/mobile-platforms.md` and are not implemented apps.


## Build a setup executable

Run `make windows-installer` from a Unix-like build environment with Go and
Python 3. It builds the Windows payloads and produces:

- `bin/Meldnet-windows-amd64-setup.exe`
- `bin/Meldnet-windows-arm64-setup.exe`

The builder uses NSIS 3.08+ (`makensis`) if available, otherwise it builds an
isolated compiler image using running Docker. Only the initial Docker image
build downloads compiler packages; compilation runs without networking. Set
`MAKENSIS=/path/to/makensis` to use a particular compiler. With existing ZIP
payloads, `python3 scripts/build-windows-installer.py` also runs directly on
Windows (set `MAKENSIS` to `makensis.exe`), macOS or Linux. `--docker` forces the
container path. `make windows-installer-test` tests payload validation without
running an installer. NSIS is a build dependency only.

Double-click the setup matching the native OS architecture and approve UAC.
On its account page, enter the **individual user's SID**, obtained by running
`whoami /user` as the user who will control the VPN. This may differ from the
administrator supplying UAC credentials. Setup validates the SID through the
existing installer, checks Wintun's signature, installs the daemon/CLI/GUI and
README under native Program Files/Meldnet, and starts the automatic LocalSystem
service. Its temporary payload gets an administrator/System-only protected ACL
before scripts or binaries are extracted. PowerShell uses its native Windows
path with no profile and a 120-second inactivity timeout. Setup rejects the
wrong native architecture and does not support silent installation.

The setup is an **unsigned development installer**, not a production-signed
release. It supports fresh installs only, refuses an existing service or payload,
and does not install an uninstaller, shortcuts, or login startup. On failure,
review setup details: partial installed files or a service may remain. Preserve
state and resolve the problem explicitly before retrying; no blanket rollback
removes identities or potentially active services. Never delete ProgramData state
as an upgrade workaround. Native Windows UAC, architecture detection, staging
ACLs, service startup, timeout and failure behavior still need disposable-VM tests.

For manual removal, first disconnect all networks through the authorized app/API
and resolve any reported cleanup failure. In an elevated PowerShell, stop the
service with `Stop-Service Meldnet`, verify it is stopped with `Get-Service
Meldnet`, and delete its registration with `sc.exe delete Meldnet`. Quit the GUI
and remove the installed program files only after successful cleanup. Preserve
`ProgramData/Meldnet` (keys, network membership and recovery journals) for recovery
or future reinstall. Do not remove state or journals if cleanup failed. For an
upgrade, preserve the existing service arguments/authorized SID, stop the service,
replace binaries and the architecture-matched signed DLL, then restart it.
