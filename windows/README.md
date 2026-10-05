# Windows daemon, CLI and TUI (experimental)

Windows 10/11 amd64 and arm64 binaries are included in `make cross`, with CGO
turned off. `make windows-package` builds architecture-specific ZIPs containing
the daemon, CLI/TUI, official signed Wintun 0.14.1 DLL, its redistribution license
and a PowerShell installer. The build checks the archive's published SHA-256;
the installer also checks the DLL's Authenticode signature. Meldnet binaries are
currently unsigned development builds. No Windows graphical app is included.

Run the extracted `install.ps1 -AllowedSID S-1-...` in Windows PowerShell 5.1
with administrator authorization. Pass the SID of the *individual user* who will
control the VPN (that user can obtain it from WindowsIdentity.GetCurrent().User).
The script installs under Program Files and creates/starts an automatic
LocalSystem service. It refuses an existing service; upgrades need an explicit
stop, preservation of its SID/service arguments, binary replacement and restart.
Never delete state to upgrade. Installation is not performed by build or tests.

The authorized user runs `meldnet.exe networks`, `meldnet.exe tui`, or other
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

Cross-compilation verifies buildability only. Native Windows named-pipe access,
DACLs, SCM installation, Wintun lifetime, encrypted IPv4/IPv6, crash cleanup,
NRPT policy activation and external-edit preservation must pass in a disposable
Windows VM before production support is claimed. This development machine has
not run a Windows tunnel or installer. Mobile platforms are assessed in
`docs/mobile-platforms.md` and are not implemented apps.
