#!/usr/bin/env bash
# Installs only an explicitly selected V2bX-2 release. Never starts/restarts a node.
set -euo pipefail
repo=shmily2-1/V2bX-2
version=${1:-}
if [[ ! "$version" =~ ^v[0-9][a-zA-Z0-9._-]*$ ]]; then
  echo "Usage: sudo bash install.sh v<release-version>" >&2
  echo "Select a published release from https://github.com/$repo/releases" >&2
  exit 2
fi
[[ $(id -u) == 0 ]] || { echo 'Run as root.' >&2; exit 1; }
[[ $(uname -s) == Linux ]] || { echo 'Linux only.' >&2; exit 1; }
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo 'Supported: linux amd64/arm64.' >&2; exit 1 ;;
esac
for tool in curl unzip sha256sum install; do command -v "$tool" >/dev/null || { echo "Missing: $tool" >&2; exit 1; }; done
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
asset="V2bX-linux-$arch.zip"
base="https://github.com/$repo/releases/download/$version"
curl --fail --location --proto '=https' --tlsv1.2 "$base/$asset" -o "$tmp/$asset"
curl --fail --location --proto '=https' --tlsv1.2 "$base/$asset.sha256" -o "$tmp/$asset.sha256"
(cd "$tmp" && sha256sum --check "$asset.sha256")
unzip -q "$tmp/$asset" V2bX -d "$tmp/extracted"
"$tmp/extracted/V2bX" version
target=/usr/local/bin/V2bX
if [[ -f "$target" ]]; then
  cp -p -- "$target" "$target.backup.$(date +%Y%m%d%H%M%S)"
fi
install -m 0755 "$tmp/extracted/V2bX" "$target.new"
mv -f -- "$target.new" "$target"
echo "Installed $version into $target. Configuration was NOT modified."
echo 'No service was installed, started or restarted. Review docs/UPGRADE.md before switching an existing node.'
echo 'If systemd uses /usr/local/V2bX/V2bX, update that service path yourself after backing it up.'
