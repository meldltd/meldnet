#!/bin/sh
set -eu
cd "$(dirname "$0")"
revision=2fec12a6e1f6e3460b6ee483aa00ad29cddadab1
path=Vendor/wireguard-apple
if [ ! -d "$path/.git" ]; then
  mkdir -p Vendor
  git clone https://git.zx2c4.com/wireguard-apple "$path"
fi
if [ "$(git -C "$path" rev-parse HEAD)" != "$revision" ] && [ -n "$(git -C "$path" status --porcelain)" ]; then
  echo 'WireGuard checkout has local changes; refusing to overwrite it.' >&2
  exit 1
fi
git -C "$path" checkout --detach "$revision"
test "$(git -C "$path" rev-parse HEAD)" = "$revision"

# Upstream uses iOS 15/macOS 12 constants requiring PackageDescription 5.5.
if git -C "$path" apply --check ../../wireguard-package.patch 2>/dev/null; then
  git -C "$path" apply ../../wireguard-package.patch
else
  git -C "$path" apply --reverse --check ../../wireguard-package.patch
fi

# Refuse additional tracked modifications beyond the reviewed compatibility patch.
if ! git -C "$path" diff | cmp -s - wireguard-package.patch; then
  echo "Unexpected changes in the pinned WireGuard checkout." >&2
  exit 1
fi

# Reuse the reviewed native transport source without importing unrelated Android
# DNS/backend dependencies or changing upstream WireGuard's module versions.
bridge="$path/Sources/WireGuardKitGo"
mkdir -p "$bridge/meldnetcontrol"
cp ../native/control/control.go "$bridge/meldnetcontrol/control.go"
cp Native/meldnet-control.go.txt "$bridge/meldnet-control.go"

# Only these two generated additions may be compiled alongside the pinned tree.
git -C "$path" ls-files --others --exclude-standard | while IFS= read -r file; do
  case "$file" in
    Sources/WireGuardKitGo/meldnet-control.go|Sources/WireGuardKitGo/meldnetcontrol/control.go) ;;
    *) echo "Unexpected untracked file in WireGuard checkout." >&2; exit 1 ;;
  esac
done
