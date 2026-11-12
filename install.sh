#!/usr/bin/env bash
# Original-style entrypoint adapted from wyx2685/V2bX-script (MPL-2.0).
# No upstream installer, telemetry, firewall reset or third-party optimizer is run.
set -euo pipefail
repo=shmily2-1/V2bX-2
script_ref=v0.1.0-core-upgrade.14
version=$script_ref
destdir=
install_dir=
no_deps=0
interactive=1
version_set=0
tmp=
die() { echo "ERROR: $*" >&2; exit 1; }
usage() {
  cat <<'HELP'
用法：bash install.sh [v版本标签] [选项]
  --no-deps           不安装缺少的依赖（管理菜单/配置向导需要 python3）
  --install-dir DIR   指定实际程序目录，须与现有服务 ExecStart 一致
  --non-interactive   只安装程序，不进入节点配置
  --destdir DIR       隔离安装验收，不安装依赖、不执行宿主服务命令
  -h, --help          显示帮助
默认 v0.1.0-core-upgrade.14，Linux amd64/arm64 + systemd。
兼容原项目的 V2bX/v2bx 菜单；不清空防火墙，不覆盖配置/证书/已有服务。
一键入口仅支持正在运行的 systemd；OpenRC/SysVinit/runit 不支持。
HELP
}
while (($#)); do
  case $1 in
    --no-deps) no_deps=1; shift ;;
    --non-interactive) interactive=0; shift ;;
    --destdir|--install-dir)
      (($# >= 2)) || die "缺少 $1 参数"
      [[ $2 =~ ^/[a-zA-Z0-9_./-]+$ ]] || die '路径必须是无空格/特殊字符的绝对路径'
      if [[ $1 == --destdir ]]; then destdir=$2; else install_dir=$2; fi
      shift 2 ;;
    v*)
      ((version_set == 0)) || die '只能指定一个版本'
      version=$1; version_set=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "未知参数 $1；请用 --help" ;;
  esac
done
[[ $version =~ ^v[0-9][a-zA-Z0-9._-]*$ ]] || die '无效版本标签'
[[ $(uname -s) == Linux ]] || die '仅支持 Linux'
case $(uname -m) in x86_64|amd64|arm64|aarch64) ;; *) die '仅支持 amd64/arm64' ;; esac
if [[ -z $destdir ]]; then
  [[ $(id -u) == 0 ]] || die '请以 root 执行（sudo bash install.sh）'
  command -v systemctl >/dev/null || die '一键入口需要 systemd；OpenRC/SysVinit/runit 请改用手动二进制部署。'
  [[ -d /run/systemd/system ]] || die '一键入口需要正在运行的 systemd；请勿在普通容器中执行。'
else
  interactive=0
fi
fetch() {
  if command -v curl >/dev/null; then
    curl -fsSL --retry 3 --connect-timeout 15 --max-time 180 --proto '=https' --proto-redir '=https' "$1" -o "$2"
  elif command -v wget >/dev/null; then
    wget --https-only --timeout=30 --tries=3 -qO "$2" "$1"
  else
    die '请先安装 wget 或 curl，以及 CA 证书'
  fi
}
cleanup() {
  local result=$?
  if [[ -n $tmp ]]; then rm -f -- "$tmp/install.sh"; rmdir -- "$tmp"; fi
  return "$result"
}
trap cleanup EXIT
tmp=$(mktemp -d)
# The bootstrap trusts the pinned repository tag, not an arbitrary mirror.
# Installed management files and executable then come from the checked ZIP.
fetch "https://raw.githubusercontent.com/$repo/$script_ref/scripts/install.sh" "$tmp/install.sh"
bash -n "$tmp/install.sh"
args=("$version" --with-systemd --with-manager)
[[ -z $destdir ]] || args+=(--destdir "$destdir")
[[ -z $install_dir ]] || args+=(--install-dir "$install_dir")
if ((no_deps == 0)) && [[ -z $destdir ]]; then args+=(--install-deps); fi
bash "$tmp/install.sh" "${args[@]}"
if [[ -z $destdir ]]; then
  if ((interactive)) && [[ -t 0 && -t 1 && ! -e /etc/V2bX/config.json ]]; then
    /usr/local/bin/v2bx generate
  else
    echo '安装完成，运行 v2bx 打开管理菜单。'
  fi
fi
