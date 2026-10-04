# Sourced only by Installer's root preinstall/postinstall scripts.
set -euo pipefail
export PATH=/usr/bin:/bin:/usr/sbin:/sbin
umask 077
fail() { echo "Meldnet installer: $*" >&2; exit 1; }

# Refuse links and non-root/writable locations before Installer writes payloads.
secure_path() {
    local path="$1" mode
    [[ ! -L "$path" ]] || fail "Refusing symbolic link: $path"
    if [[ -e "$path" ]]; then
        [[ $(/usr/bin/stat -f %u "$path") == 0 ]] || fail "Must be owned by root: $path"
        mode=$(/usr/bin/stat -f %Lp "$path")
        (( (8#$mode & 0022) == 0 )) || fail "Must not be group/world writable: $path"
        if [[ -f "$path" ]]; then
            [[ $(/usr/bin/stat -f %l "$path") == 1 ]] || fail "Refusing hard-linked file: $path"
        fi
    fi
}

check_target() {
    [[ $(/usr/bin/id -u) == 0 ]] || fail 'Administrator authorization is required.'
    [[ ${3:-} == / ]] || fail 'Install on the running Mac startup disk only.'
    for path in /Library /Library/PrivilegedHelperTools /Library/LaunchDaemons /Library/LaunchAgents /Library/Logs /Library/Logs/Meldnet /Library/Logs/Meldnet/daemon.log \
        /Library/PrivilegedHelperTools/si.meldnet.daemon \
        /Library/LaunchDaemons/si.meldnet.daemon.plist \
        /Library/LaunchAgents/si.meldnet.menubar.pkg.plist; do
        secure_path "$path"
    done
    [[ -d /Applications && ! -L /Applications ]] || fail 'Invalid Applications directory.'
    [[ $(/usr/bin/stat -f %u /Applications) == 0 ]] || fail 'Applications must be root-owned.'
    local app_mode
    app_mode=$(/usr/bin/stat -f %Lp /Applications)
    (( (8#$app_mode & 0002) == 0 )) || fail 'Applications must not be world-writable.'
    # This simple bundle has no symlinks. Refuse redirected payload destinations.
    if [[ -e /Applications/Meldnet.app || -L /Applications/Meldnet.app ]]; then
        secure_path /Applications/Meldnet.app
        [[ -d /Applications/Meldnet.app ]] || fail 'Meldnet.app must be a directory.'
        local suspicious
        suspicious=$(/usr/bin/find /Applications/Meldnet.app \( -type l -o ! -user root -o -perm -0022 -o \( -type f -links +1 \) \) -print -quit)
        [[ -z "$suspicious" ]] || fail 'Existing Meldnet.app has unsafe ownership, permissions or links.'
    fi
}

allowed_uid() {
    local uid executable
    local plist=/Library/LaunchDaemons/si.meldnet.daemon.plist
    if [[ -e "$plist" ]]; then
        # Preserve the authorized UID on upgrade. Custom commands/config paths
        # require manual migration; never silently reset them to default state.
        executable=$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' "$plist")
        [[ "$executable" == /Library/PrivilegedHelperTools/si.meldnet.daemon || "$executable" == /usr/local/libexec/meldnetd ]] || fail 'Unrecognized daemon command; migrate the custom service first.'
        [[ $(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:1' "$plist") == --allow-uid ]] || fail 'Custom daemon arguments require manual migration.'
        if /usr/libexec/PlistBuddy -c 'Print :ProgramArguments:3' "$plist" >/dev/null 2>&1; then
            fail 'Custom daemon arguments require manual migration.'
        fi
        uid=$(/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:2' "$plist")
    else
        uid=$(/usr/bin/stat -f %u /dev/console)
    fi
    [[ "$uid" =~ ^[0-9]+$ && ${#uid} -le 10 ]] || fail 'Invalid authorized user ID.'
    (( uid >= 501 && uid < 2147483647 )) || fail 'Log in to the intended desktop account before installing.'
    /usr/bin/id -un "$uid" >/dev/null 2>&1 || fail 'The authorized desktop account no longer exists.'
    echo "$uid"
}
