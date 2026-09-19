#!/usr/bin/env bash
# Install verified release assets only. Never start/restart a node or modify its config.
set -euo pipefail
umask 022

repo=shmily2-1/V2bX-2
# Pin an acceptance release; GitHub /latest excludes prereleases.
version=v0.1.0-core-upgrade.6
install_dir=
destdir=
with_systemd=0
with_manager=0
install_deps=0
tmp=
staged=
unit_staged=

die() { echo "ERROR: $*" >&2; exit 1; }
usage() {
  cat <<'USAGE'
Usage: bash install.sh [v<release-version>] [options]
  --with-systemd       Create an inactive V2bX.service and config.json.example if absent
  --with-manager       Install original-style V2bX/v2bx menu (requires --with-systemd)
  --install-deps       Install missing tools using apt-get/dnf/yum
  --install-dir DIR    Absolute binary directory (default: existing service/legacy path,
                      otherwise /usr/local/bin). Existing symlinks are preserved.
  --destdir DIR        Stage into an isolated root, without root or host service changes
                      (packaging/tests only; incompatible with --install-deps)
  -h, --help           Show this help
Default release: v0.1.0-core-upgrade.6 (prerelease / acceptance testing)
Never changes existing configuration, enables services, or starts/restarts nodes.
USAGE
}
version_set=0
while (($#)); do
  case "$1" in
    --with-systemd) with_systemd=1; shift ;;
    --with-manager) with_manager=1; shift ;;
    --install-deps) install_deps=1; shift ;;
    --install-dir|--destdir)
      (($# >= 2)) || die "Missing value for $1"
      if [[ $1 == --install-dir ]]; then install_dir=$2; else destdir=$2; fi
      [[ $2 =~ ^/[a-zA-Z0-9_./-]+$ ]] || die 'Paths must be absolute without spaces or shell/systemd metacharacters.'
      shift 2 ;;
    -h|--help) usage; exit 0 ;;
    v*)
      ((version_set == 0)) || die 'Only one version is allowed.'
      version=$1; version_set=1; shift ;;
    *) die "Unknown argument: $1 (see --help)" ;;
  esac
done
[[ $version =~ ^v[0-9][a-zA-Z0-9._-]*$ ]] || die 'Invalid release version.'
((with_manager == 0 || with_systemd == 1)) || die '--with-manager requires --with-systemd.'
[[ $(uname -s) == Linux ]] || die 'Linux only.'
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die 'Supported architectures: Linux amd64 / arm64.' ;;
esac
if [[ -z $destdir ]]; then
  [[ $(id -u) == 0 ]] || die 'Run as root (sudo bash install.sh ...).'
else
  ((install_deps == 0)) || die '--install-deps is forbidden with --destdir.'
fi

missing=()
tools=(curl unzip sha256sum install realpath flock mktemp)
packages=(ca-certificates curl unzip coreutils util-linux)
if ((with_manager)); then
  tools+=(python3 openssl ss ip)
  packages+=(python3 openssl)
  if command -v apt-get >/dev/null; then packages+=(iproute2); else packages+=(iproute); fi
fi
for tool in "${tools[@]}"; do
  command -v "$tool" >/dev/null || missing+=("$tool")
done
if ((${#missing[@]})); then
  ((install_deps == 1)) || die "Missing tools: ${missing[*]}. Install them or use --install-deps."
  if command -v apt-get >/dev/null; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${packages[@]}"
  elif command -v dnf >/dev/null; then
    dnf install -y "${packages[@]}"
  elif command -v yum >/dev/null; then
    yum install -y "${packages[@]}"
  else
    die "No supported package manager. Install manually: ${missing[*]}"
  fi
  for tool in "${missing[@]}"; do command -v "$tool" >/dev/null || die "Still missing: $tool"; done
fi
if [[ -n $destdir ]]; then
  destdir=$(realpath -m -- "$destdir")
  [[ $destdir != / ]] || die '--destdir cannot be the host root.'
fi
# Resolve symlinks, but never let staging escape its isolated root.
resolve() {
  local resolved
  resolved=$(realpath -m -- "$destdir$1") || return 1
  if [[ -n $destdir && $resolved != "$destdir/"* ]]; then
    echo "ERROR: Path escapes --destdir: $1" >&2
    return 1
  fi
  printf '%s\n' "$resolved"
}

service_exec=
service_loaded=0
if [[ -z $destdir ]] && command -v systemctl >/dev/null; then
  load_state=$(systemctl show V2bX.service --property=LoadState --value 2>/dev/null || true)
  if [[ -n $load_state && $load_state != not-found ]]; then
    service_loaded=1
    exec_info=$(systemctl show V2bX.service --property=ExecStart --value 2>/dev/null || true)
    if [[ $exec_info =~ path=([^\ ;]+) ]]; then service_exec=${BASH_REMATCH[1]}; fi
    if [[ -z $install_dir && -z $service_exec ]]; then
      die 'Existing service has no readable ExecStart. Inspect systemctl cat V2bX; pass --install-dir explicitly.'
    fi
  fi
fi
if [[ -z $install_dir ]]; then
  if [[ -n $service_exec ]]; then
    [[ $service_exec =~ ^/[a-zA-Z0-9_./-]+/V2bX$ ]] || die 'Custom ExecStart detected. Inspect it and pass --install-dir explicitly.'
    install_dir=${service_exec%/*}
  else
    modern=$(resolve /usr/local/bin/V2bX)
    legacy=$(resolve /usr/local/V2bX/V2bX)
    if [[ -e $modern && -e $legacy && $modern != "$legacy" ]]; then
      die 'Both modern and legacy binaries exist. Inspect ExecStart and select --install-dir explicitly.'
    fi
    if [[ -e $legacy ]]; then
      install_dir=/usr/local/V2bX
    elif [[ -e $modern ]]; then
      install_dir=/usr/local/bin
    elif ((with_manager)); then
      install_dir=/usr/local/V2bX
    else
      install_dir=/usr/local/bin
    fi
  fi
fi
target=$(resolve "$install_dir/V2bX")
[[ ${target##*/} == V2bX ]] || die 'Refusing a binary symlink pointing to a non-V2bX file.'
if [[ $target != "$(resolve "$install_dir")/V2bX" &&
      $target != "$(resolve /usr/local/V2bX)/V2bX" &&
      $target != "$(resolve /usr/local/bin)/V2bX" ]]; then
  die 'Custom binary symlink destination: inspect it and pass its actual directory with --install-dir.'
fi
[[ ! -d $target ]] || die "Binary target is a directory: $target"
if [[ -n $service_exec && $(resolve "$service_exec") != "$target" ]]; then
  die 'Selected directory differs from the service ExecStart. Refusing an ineffective update.'
fi

config_dir=
unit=
create_unit=0
if ((with_systemd)); then
  if [[ -z $destdir ]]; then
    command -v systemctl >/dev/null || die '--with-systemd requires systemd; omit it for binary-only installation.'
    [[ -d /run/systemd/system ]] || die 'systemd is not running; omit --with-systemd (e.g. in a container).'
  fi
  config_dir=$(resolve /etc/V2bX)
  unit="$destdir/etc/systemd/system/V2bX.service"
  resolve /etc/systemd/system >/dev/null
  create_unit=1
  for unit_dir in /etc/systemd/system /run/systemd/system /usr/lib/systemd/system /lib/systemd/system; do
    if [[ -e $destdir$unit_dir/V2bX.service || -L $destdir$unit_dir/V2bX.service ]]; then create_unit=0; fi
  done
  ((service_loaded == 0)) || create_unit=0
  [[ ! -e $config_dir || -d $config_dir ]] || die 'Configuration directory is not a directory.'
fi

manager_files=(V2bX.sh initconfig.sh configure.py provision.py system-tools.py install.sh bootstrap.sh)
runtime_assets=(geoip.dat geosite.dat geoip.db geosite.db dns.json route.json custom_inbound.json custom_outbound.json)
manager_state=
manager_entry=
manager_alias=
if ((with_manager)); then
  manager_state=$(resolve /usr/local/lib/V2bX-2)
  manager_entry="$(resolve /usr/bin)/V2bX"
  manager_alias="$(resolve /usr/local/bin)/v2bx"
  [[ $target != "$manager_entry" ]] || die 'Service uses /usr/bin/V2bX as a binary; migrate it before installing the menu.'
  # Reject symlinked state files, unknown command collisions and escaped parents.
  for name in "${manager_files[@]}" binary-path .install.lock; do
    [[ ! -L $manager_state/$name ]] || die "Symlinked management state: $name"
    [[ ! -e $manager_state/$name || -f $manager_state/$name ]] || die "Invalid management state: $name"
  done
  for path in "$manager_entry" "$manager_alias" "$destdir/usr/bin/v2bx"; do
    if [[ -e $path || -L $path ]]; then
      resolved=$(realpath -m -- "$path")
      if [[ -L $path && ( $resolved == "$manager_state/V2bX.sh" || $resolved == "$manager_entry" ) ]]; then continue; fi
      # Only known V2bX shell launchers may be replaced, never arbitrary binaries.
      [[ -f $path && ! -L $path ]] || die "Unknown command symlink: $path"
      head -c 256 "$path" | grep -aq '^#!.*\(bash\|/sh\)' || die "Refusing to replace non-shell command: $path"
      grep -aq 'V2bX' "$path" || die "Refusing unknown command: $path"
    fi
  done
fi

asset="V2bX-linux-$arch.zip"
cleanup() {
  local result=$?
  [[ -z $unit_staged ]] || rm -f -- "$unit_staged"
  [[ -z $staged ]] || rm -f -- "$staged"
  if [[ -n $tmp ]]; then
    rm -f -- "$tmp/V2bX" "$tmp/$asset" "$tmp/$asset.sha256" "$tmp/config.json.example"
    for name in "${manager_files[@]}"; do rm -f -- "$tmp/manager-$name"; done
    for name in "${runtime_assets[@]}"; do rm -f -- "$tmp/runtime-$name"; done
    rmdir -- "$tmp"
  fi
  return "$result"
}
trap cleanup EXIT
tmp=$(mktemp -d)
base="https://github.com/$repo/releases/download/$version"
download() {
  curl --fail --silent --show-error --location --retry 3 --connect-timeout 15 --max-time 600 \
    --proto '=https' --proto-redir '=https' --tlsv1.2 "$1" -o "$2"
}
echo "Installing $repo $version ($arch). Acceptance releases are not production guarantees."
download "$base/$asset" "$tmp/$asset"
download "$base/$asset.sha256" "$tmp/$asset.sha256"
# Never allow malformed checksum files to make sha256sum read arbitrary local paths.
mapfile -t checksum_lines < "$tmp/$asset.sha256"
[[ ${#checksum_lines[@]} == 1 ]] || die 'Expected exactly one checksum entry.'
read -r expected filename extra <<< "${checksum_lines[0]}"
[[ $expected =~ ^[a-fA-F0-9]{64}$ && ( $filename == "$asset" || $filename == "*$asset" ) && -z $extra ]] || die 'Invalid checksum entry.'
actual=$(sha256sum "$tmp/$asset")
[[ ${actual%% *} == "${expected,,}" ]] || die 'SHA256 mismatch; nothing was installed.'
# Extract only known entries to files we create; never unpack arbitrary archive paths.
unzip -p "$tmp/$asset" V2bX > "$tmp/V2bX"
chmod 0755 "$tmp/V2bX"
"$tmp/V2bX" version || die 'Downloaded binary could not run; existing installation is unchanged (check CPU / noexec TMPDIR).'
if ((with_systemd)); then
  unzip -p "$tmp/$asset" example/hysteria2-xboard.json > "$tmp/config.json.example"
fi
if ((with_manager)); then
  for name in "${runtime_assets[@]}"; do
    if [[ ! -e $config_dir/$name && ! -L $config_dir/$name ]]; then
      unzip -p "$tmp/$asset" "example/$name" > "$tmp/runtime-$name" || die "Release lacks example/$name"
      [[ -s $tmp/runtime-$name ]] || die "Empty runtime asset $name"
    fi
  done
fi
if ((with_manager)); then
  for name in "${manager_files[@]}"; do
    unzip -p "$tmp/$asset" "management/$name" > "$tmp/manager-$name" || die 'Release lacks the adapted management bundle; use a supported release.'
    [[ -s $tmp/manager-$name ]] || die "Empty management file $name"
    if [[ $name == *.sh ]]; then bash -n "$tmp/manager-$name"; fi
  done
  python3 -c 'import ast,sys; ast.parse(open(sys.argv[1], encoding="utf-8").read())' "$tmp/manager-configure.py"
  python3 -c 'import ast,sys; ast.parse(open(sys.argv[1], encoding="utf-8").read())' "$tmp/manager-provision.py"
  python3 -c 'import ast,sys; ast.parse(open(sys.argv[1], encoding="utf-8").read())' "$tmp/manager-system-tools.py"
fi

mkdir -p -- "${target%/*}"
exec 9>"${target%/*}/.V2bX.install.lock"
flock -n 9 || die 'Another installation is in progress.'
staged=$(mktemp "${target%/*}/.V2bX.new.XXXXXX")
install -m 0755 "$tmp/V2bX" "$staged"
if [[ -e $target ]]; then
  [[ -f $target ]] || die 'Existing binary is not a regular file.'
  backup=$(mktemp "$target.backup.$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")
  cp -p -- "$target" "$backup"
  echo "Backup: $backup"
fi
if ((with_systemd)); then
  [[ -d $config_dir ]] || install -d -m 0750 "$config_dir"
  example="$config_dir/config.json.example"
  if [[ ! -e $example && ! -L $example ]]; then
    (umask 077; set -C; cat "$tmp/config.json.example" > "$example")
  fi
  if ((create_unit)); then
    mkdir -p -- "${unit%/*}"
    unit_staged=$(mktemp "${unit%/*}/.V2bX.service.XXXXXX")
    cat > "$unit_staged" <<UNIT
[Unit]
Description=V2bX-2 node service
Wants=network-online.target
After=network-online.target
ConditionPathExists=/etc/V2bX/config.json

[Service]
Type=simple
User=root
WorkingDirectory=/etc/V2bX
ExecStart=$install_dir/V2bX server --config /etc/V2bX/config.json
Restart=on-failure
RestartSec=5
LimitNOFILE=1048576
UMask=0077

[Install]
WantedBy=multi-user.target
UNIT
    chmod 0644 "$unit_staged"
    # Publish a complete unit without overwriting a concurrent/masked unit.
    ln -- "$unit_staged" "$unit"
    rm -f -- "$unit_staged"
    unit_staged=
    echo 'Created V2bX.service, but did NOT enable or start it.'
  else
    echo 'Existing V2bX.service was preserved unchanged.'
  fi
fi

# Management files are drawn from the same verified ZIP, not mutable raw URLs.
if ((with_manager)); then
  mkdir -p -- "$manager_state" "${manager_alias%/*}" "${manager_entry%/*}"
  exec 8>"$manager_state/.install.lock"
  flock -n 8 || die 'Another management installation is in progress.'
  for name in "${manager_files[@]}"; do
    path="$manager_state/$name"
    if [[ -f $path ]]; then
      saved=$(mktemp "$path.backup.XXXXXX")
      cp -p -- "$path" "$saved"
    fi
    unit_staged=$(mktemp "$manager_state/.new.XXXXXX")
    install -m 0755 "$tmp/manager-$name" "$unit_staged"
    mv -fT -- "$unit_staged" "$path"
    unit_staged=
  done
  unit_staged=$(mktemp "$manager_state/.path.XXXXXX")
  printf '%s\n' "${target#"$destdir"}" > "$unit_staged"
  chmod 0644 "$unit_staged"
  mv -fT -- "$unit_staged" "$manager_state/binary-path"
  unit_staged=
  # Links are relative, so --destdir output is usable as an isolated root image.
  for path in "$manager_entry" "$manager_alias"; do
    if [[ -e $path || -L $path ]]; then
      saved=$(mktemp "$path.backup.XXXXXX")
      cp -Pp -- "$path" "$saved"
    fi
    unit_staged=$(mktemp "${path%/*}/.V2bX.link.XXXXXX")
    rm -f -- "$unit_staged"
    link=$(realpath --relative-to="${path%/*}" "$manager_state/V2bX.sh")
    ln -s -- "$link" "$unit_staged"
    mv -fT -- "$unit_staged" "$path"
    unit_staged=
  done
  path="$destdir/usr/bin/v2bx"
  if [[ -e $path || -L $path ]]; then
    saved=$(mktemp "$path.backup.XXXXXX")
    cp -Pp -- "$path" "$saved"
  fi
  unit_staged=$(mktemp "${manager_entry%/*}/.v2bx.link.XXXXXX")
  rm -f -- "$unit_staged"
  ln -s -- V2bX "$unit_staged"
  mv -fT -- "$unit_staged" "$path"
  unit_staged=
fi

if ((with_manager)); then
  for name in "${runtime_assets[@]}"; do
    if [[ ! -e $config_dir/$name && ! -L $config_dir/$name ]]; then
      mode=0644
      [[ $name == *.json ]] && mode=0600
      install -m "$mode" "$tmp/runtime-$name" "$config_dir/$name"
    fi
  done
fi

# Commit the executable last. Filesystem failures above leave the old binary intact.
mv -f -- "$staged" "$target"
staged=
echo "Installed $version into $target. Existing configuration was NOT modified."
if ((with_systemd)); then
  if ((create_unit)) && [[ -z $destdir ]]; then
    if ! systemctl daemon-reload; then
      echo 'WARNING: Binary and unit installed, but systemd reload failed. Run systemctl daemon-reload before starting.' >&2
    fi
  fi
  echo 'New node: review /etc/V2bX/config.json.example, create config.json, fill panel/key/node/certificate values.'
  echo 'After validation only: systemctl enable --now V2bX (new node), or systemctl restart V2bX (update).'
fi
[[ -z $destdir ]] || echo "Staging only: $destdir. No host service commands were executed."
echo 'No node was started/restarted. No firewall rules or certificates were changed.'
if ((with_manager)); then
  echo 'Management menu: v2bx (or /usr/bin/V2bX). Existing /usr/local/bin/V2bX binaries are preserved.'
fi
echo 'Hysteria2 hopping also requires the Xboard patch, nftables/iptables and UDP security-group rules; see docs/HYSTERIA2-PORT-HOPPING.md.'
