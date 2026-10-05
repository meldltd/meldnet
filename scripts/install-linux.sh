#!/bin/sh
# Explicit administrator installation. Never called by the GUI or daemon.
set -eu
if [ "$(id -u)" != 0 ]; then echo 'Run this installer as root with the authorized numeric user UID.' >&2; exit 1; fi
uid=${1:?Usage: install-linux.sh ALLOWED_UID [PAYLOAD_DIRECTORY]}
case "$uid" in ''|*[!0-9]*) echo 'ALLOWED_UID must be numeric' >&2; exit 1;; esac
payload=${2:-$(CDPATH= cd -- "$(dirname -- "$0")/../linux" && pwd)}
install -d -m 0755 /usr/local/lib/meldnet /usr/local/share/applications
install -m 0644 "$payload/meldnet_gui.py" /usr/local/lib/meldnet/meldnet_gui.py
install -m 0644 "$payload/meldnet.desktop" /usr/local/share/applications/meldnet.desktop
# A daemon binary is installed separately; activation is an explicit admin action.
sed "s/@UID@/$uid/g" "$payload/meldnetd.service.in" > /etc/systemd/system/meldnetd.service
chmod 0644 /etc/systemd/system/meldnetd.service
printf '%s\n' 'Installed desktop app and service definition. Install meldnetd in /usr/local/sbin, then explicitly enable/start meldnetd.service.'
