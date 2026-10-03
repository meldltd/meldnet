# Meldnet agent rules

Read `handoff.md` and `docs/architecture.md` before changing code. Update
`handoff.md` before handing work back, including exact verification results and
known limitations. It is the durable checkpoint between sessions, not a transcript.

## Boundaries

- The VPN must work without any GUI/TUI process. `cmd/meldnetd` owns keys,
  persistence, tunnel lifecycle, and operating-system integration.
- `internal/config`, `internal/vpn`, and `internal/service` must never import
  Bubble Tea, Bubbles, Lip Gloss, or a presentation package.
- The TUI calls the versioned local API. It must not open private configuration
  files, run privileged commands, or launch/stop the daemon implicitly.
- macOS and Linux are the supported targets. Keep OS-specific code isolated.
- Use established WireGuard implementations; do not implement cryptography.
- Embed wireguard-go. The production daemon must not depend on or launch external
  tools (wg, wg-quick, ip, ifconfig, route, shell). Networking dependencies must be
  compiled Go libraries or built-in OS facilities. Keep CGO disabled for releases.
- TUN interfaces are non-persistent. Preserve endpoint-route recovery journals
  until cleanup succeeds; never claim a userspace tunnel survives daemon death.
- Private DNS must bind only the primary VPN address or a client loopback address.
  Never forward private-zone misses to public resolvers. Journal host resolver
  changes before writing, preserve external edits, and restore settings on down.
  Tests must use temporary resolver paths or isolated containers, never host DNS.
- Never report a simulated tunnel as a real connection or an interface being up
  as proof that a peer is reachable.

## Security and changes

- WireGuard and TLS private keys stay in owner-only files and never enter API responses, logs,
  command arguments, fixtures containing real credentials, or the UI.
- Enrollment secrets may cross the authorized local API once on issuance/input;
  never log them or accept them in command arguments. Persist only hashes on the
  primary. Keep Fiber prefork disabled: it transitively imports os/exec, but the
  application must never launch subprocesses.
- Managed clients route through the trusted primary. Do not claim direct mesh or
  end-to-end client encryption. Relay only authenticated member addresses.
- Validate every API mutation in the service, independently of UI validation.
  Accept typed configuration only; never accept wg-quick hooks or shell snippets.
- Use argument arrays for child processes, bounded timeouts, and sanitized
  diagnostics. Do not interpolate API inputs into commands.
- Do not change host routing, install a system service, or connect a real tunnel
  just to run ordinary tests. Use fakes and isolated Linux network namespaces.
- Preserve failed-cleanup state and report errors. Do not hide cleanup failures.
- Do not add a new framework or platform without a concrete requirement.

## Verification and handoff

- Run `gofmt`, `go vet ./...`, and `go test -race ./...` for Go changes.
- Run `make cross` when changing supported-platform code.
- Keep lifecycle, configuration validation, API isolation, and key privacy tested.
- Record commands actually run; distinguish cross-compilation from native tests
  and simulated tests from real encrypted traffic.
- Keep the current milestone and next actionable steps in `handoff.md`. Mark
  unfinished work explicitly; never turn a roadmap item into a claimed feature.
