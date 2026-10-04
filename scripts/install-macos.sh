#!/bin/bash
# Run as the desktop user. Root is used only to install the boot-time daemon.
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(uname -s) == Darwin && $(id -u) != 0 ]] || { echo 'Run as your normal macOS login user.' >&2; exit 1; }
[[ $# == 0 || ($# == 1 && $1 == --menu-only) ]] || { echo 'Usage: scripts/install-macos.sh [--menu-only]' >&2; exit 1; }
./scripts/build-macos-app.sh
if [[ ${1:-} != --menu-only ]]; then
  make build
  plist=$(mktemp)
  trap 'rm -f "$plist"' EXIT
  sed "s/@UID@/$(id -u)/g" packaging/si.meldnet.daemon.plist.in > "$plist"
  plutil -lint "$plist"
  sudo install -d -o root -g wheel -m 0755 /usr/local/libexec
  sudo install -o root -g wheel -m 0755 bin/meldnetd /usr/local/libexec/meldnetd
  sudo install -o root -g wheel -m 0644 "$plist" /Library/LaunchDaemons/si.meldnet.daemon.plist
  sudo launchctl enable system/si.meldnet.daemon
  echo 'Daemon installed for next boot. The currently running VPN is left in place.'
fi
mkdir -p "$HOME/Applications" "$HOME/Library/LaunchAgents"
agent="$HOME/Library/LaunchAgents/si.meldnet.menubar.plist"
if launchctl print "gui/$(id -u)/si.meldnet.menubar" >/dev/null 2>&1; then
  launchctl bootout "gui/$(id -u)/si.meldnet.menubar"
fi
# ditto replaces the bundle contents without touching VPN state.
ditto bin/Meldnet.app "$HOME/Applications/Meldnet.app"
plist_agent=$(mktemp)
trap 'rm -f "${plist:-}" "$plist_agent"' EXIT
cat > "$plist_agent" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>si.meldnet.menubar</string>
<key>ProgramArguments</key><array/>
<key>RunAtLoad</key><true/>
<key>ProcessType</key><string>Interactive</string>
<key>LimitLoadToSessionType</key><string>Aqua</string>
</dict></plist>
PLIST
# plutil handles XML escaping for usernames/paths with special characters.
plutil -insert ProgramArguments.0 -string "$HOME/Applications/Meldnet.app/Contents/MacOS/Meldnet" "$plist_agent"
plutil -lint "$plist_agent"
install -m 0644 "$plist_agent" "$agent"
launchctl enable "gui/$(id -u)/si.meldnet.menubar"
launchctl bootstrap "gui/$(id -u)" "$agent"
echo 'Meldnet menu bar app installed and started; it will open at login.'
