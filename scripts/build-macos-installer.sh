#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/.."
[[ $(uname -s) == Darwin ]] || { echo 'Build the installer on macOS.' >&2; exit 1; }
version=${VERSION:-0.1.0}
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'VERSION must be X.Y.Z.' >&2; exit 1; }
code_identity=${CODE_SIGN_IDENTITY:--}
installer_identity=${INSTALLER_SIGN_IDENTITY:-}
if [[ "$code_identity" == - && -n "$installer_identity" ]] || [[ "$code_identity" != - && -z "$installer_identity" ]]; then
    echo 'For distribution, set both CODE_SIGN_IDENTITY and INSTALLER_SIGN_IDENTITY.' >&2; exit 1
fi
mkdir -p bin
stage=$(mktemp -d "$PWD/bin/.macos-installer.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
root="$stage/root"
app="$root/Applications/Meldnet.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$root/Library/PrivilegedHelperTools" "$stage/scripts"
for arch in arm64 x86_64; do
    goarch=$arch
    [[ "$arch" != x86_64 ]] || goarch=amd64
    swiftc -swift-version 5 -O -framework AppKit -target "$arch-apple-macosx13.0" macos/Meldnet/API.swift macos/Meldnet/Icon.swift macos/Meldnet/TUILauncher.swift macos/Meldnet/main.swift -o "$stage/menu-$arch"
    CGO_ENABLED=0 GOOS=darwin GOARCH="$goarch" go build -trimpath -o "$stage/daemon-$arch" ./cmd/meldnetd
    CGO_ENABLED=0 GOOS=darwin GOARCH="$goarch" go build -trimpath -o "$stage/cli-$arch" ./cmd/meldnet
done
lipo -create "$stage/menu-arm64" "$stage/menu-x86_64" -output "$app/Contents/MacOS/Meldnet"
lipo -create "$stage/daemon-arm64" "$stage/daemon-x86_64" -output "$root/Library/PrivilegedHelperTools/si.meldnet.daemon"
lipo -create "$stage/cli-arm64" "$stage/cli-x86_64" -output "$app/Contents/Resources/meldnet"
cp macos/Info.plist "$app/Contents/Info.plist"
# Go 1.27 binaries require macOS 13, including the bundled terminal client.
plutil -replace LSMinimumSystemVersion -string 13.0 "$app/Contents/Info.plist"
plutil -replace CFBundleShortVersionString -string "$version" "$app/Contents/Info.plist"
plutil -replace CFBundleVersion -string "$version" "$app/Contents/Info.plist"
find "$root" -type d -exec chmod 0755 {} +
chmod 0644 "$app/Contents/Info.plist"
chmod 0755 "$app/Contents/MacOS/Meldnet" "$app/Contents/Resources/meldnet" "$root/Library/PrivilegedHelperTools/si.meldnet.daemon"
sign_args=(--force --sign "$code_identity")
[[ "$code_identity" == - ]] || sign_args+=(--options runtime --timestamp)
codesign "${sign_args[@]}" "$app/Contents/Resources/meldnet"
codesign "${sign_args[@]}" "$root/Library/PrivilegedHelperTools/si.meldnet.daemon"
codesign "${sign_args[@]}" "$app"
codesign --verify --strict --deep "$app"
codesign --verify --strict "$root/Library/PrivilegedHelperTools/si.meldnet.daemon"
cp packaging/macos/scripts/* "$stage/scripts/"
chmod 0755 "$stage/scripts/preinstall" "$stage/scripts/postinstall"
# Never relocate to a developer's existing ~/Applications copy.
cat > "$stage/components.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0"><array><dict>
<key>RootRelativeBundlePath</key><string>Applications/Meldnet.app</string>
<key>BundleIsRelocatable</key><false/>
<key>BundleHasStrictIdentifier</key><true/>
<key>BundleIsVersionChecked</key><true/>
<key>BundleOverwriteAction</key><string>upgrade</string>
</dict></array></plist>
PLIST
pkgbuild --root "$root" --install-location / --ownership recommended \
    --identifier si.meldnet.installer --version "$version" \
    --component-plist "$stage/components.plist" --scripts "$stage/scripts" "$stage/Meldnet-component.pkg"
cat > "$stage/Distribution.xml" <<XML
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
<title>Meldnet $version</title>
<welcome file="welcome.html" mime-type="text/html"/>
<options customize="never" require-scripts="true" hostArchitectures="arm64,x86_64"/>
<domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>
<volume-check><allowed-os-versions><os-version min="13.0"/></allowed-os-versions></volume-check>
<choices-outline><line choice="default"/></choices-outline>
<choice id="default" visible="false"><pkg-ref id="si.meldnet.installer"/></choice>
<pkg-ref id="si.meldnet.installer" version="$version" auth="Root" onConclusion="None">Meldnet-component.pkg</pkg-ref>
</installer-gui-script>
XML
output="$PWD/bin/Meldnet-$version-universal.pkg"
product_args=(--distribution "$stage/Distribution.xml" --resources packaging/macos/resources --package-path "$stage")
[[ -z "$installer_identity" ]] || product_args+=(--sign "$installer_identity" --timestamp)
productbuild "${product_args[@]}" "$stage/Meldnet.pkg"
mv -f "$stage/Meldnet.pkg" "$output"
echo "Built $output"
if [[ "$code_identity" == - ]]; then
    echo 'Local development package: ad-hoc signed code, unsigned installer, not notarized.'
fi
