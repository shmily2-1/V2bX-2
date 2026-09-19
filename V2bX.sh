#!/usr/bin/env bash
# Original-style V2bX management menu, adapted from wyx2685/V2bX-script (MPL-2.0).
# Installed as /usr/bin/V2bX and /usr/local/bin/v2bx. No upstream downloads.
set -euo pipefail
script_ref=v0.1.0-core-upgrade.7
root=
if [[ ${1:-} == --root ]]; then
  [[ $# -ge 3 && $2 =~ ^/[a-zA-Z0-9_./-]+$ ]] || { echo 'Invalid --root' >&2; exit 2; }
  root=$(realpath -e -- "$2")
  [[ $root != / ]] || { echo 'Use no --root for the host' >&2; exit 2; }
  shift 2
fi
state="$root/usr/local/lib/V2bX-2"
config="$root/etc/V2bX/config.json"
self=$(realpath -e -- "${BASH_SOURCE[0]}")
binary=
fail() { printf '错误：%s\n' "$*" >&2; return 1; }
root_only() { [[ -n $root || $(id -u) == 0 ]] || { fail '请以 root 运行'; return 1; }; }
host_only() { [[ -z $root ]] || { fail '隔离模式禁止宿主服务/日志操作'; return 1; }; }
resolve() {
  local path
  path=$(realpath -m -- "$root$1") || return 1
  [[ -z $root || $path == "$root/"* ]] || { fail '路径逃逸隔离根'; return 1; }
  printf '%s\n' "$path"
}
state=$(resolve /usr/local/lib/V2bX-2)
config=$(resolve /etc/V2bX/config.json)
find_binary() {
  local logical info service_path=
  [[ -f $state/binary-path ]] || { fail '缺少安装记录，请重新执行适配安装器'; return 1; }
  IFS= read -r logical < "$state/binary-path"
  [[ $logical =~ ^/[a-zA-Z0-9_./-]+/V2bX$ ]] || { fail '无效程序路径记录'; return 1; }
  binary=$(resolve "$logical") || return 1
  [[ $binary != "$self" && ${binary##*/} == V2bX ]] || { fail '程序路径无效或指向管理脚本'; return 1; }
  if [[ ${1:-} == --allow-missing ]]; then
    [[ ! -e $binary || -f $binary ]] || { fail '程序路径不是普通文件'; return 1; }
  else
    [[ -x $binary ]] || { fail '程序不存在或不可执行'; return 1; }
  fi
  if [[ -z $root ]] && command -v systemctl >/dev/null; then
    info=$(systemctl show V2bX.service --property=ExecStart --value 2>/dev/null || true)
    if [[ $info =~ path=([^\ ;]+) ]]; then service_path=${BASH_REMATCH[1]}; fi
    if [[ -n $service_path && $(realpath -m -- "$service_path") != "$binary" ]]; then
      fail '现有服务路径与安装记录不一致，请先核对 systemctl cat V2bX'; return 1
    fi
  fi
}
validate_config() {
  [[ -f $config ]] || { fail '缺少 config.json，请先执行 v2bx generate'; return 1; }
  python3 "$state/configure.py" --check "$config"
}
control() {
  local action=$1
  root_only; host_only
  command -v systemctl >/dev/null || { fail '此管理器需要 systemd'; return 1; }
  if [[ $action == start || $action == restart ]]; then find_binary; validate_config; fi
  systemctl "$action" V2bX.service
  if [[ $action == start || $action == restart ]]; then
    sleep 3
    systemctl is-active --quiet V2bX.service || { fail '服务没有保持运行，请查看 v2bx log'; return 1; }
  fi
  printf '已执行 %s；使用 v2bx status / log 核对服务。\n' "$action"
}
status() {
  find_binary
  printf '程序：%s\n' "$binary"
  "$binary" version
  if [[ -z $root ]]; then systemctl status V2bX.service --no-pager --full; else echo '隔离安装，不查询宿主服务。'; fi
}
generate() {
  root_only
  if [[ -n $root ]]; then
    python3 "$state/configure.py" --config "$config" --root "$root" "$@"
  else
    bash "$state/initconfig.sh" "$@"
  fi
}
edit() {
  root_only
  # Edit a private temporary copy; do not let the server's file watcher read half a JSON.
  local editor=${EDITOR:-vi}
  if [[ -n $root ]]; then
    python3 "$state/configure.py" --config "$config" --root "$root" --edit "$editor"
  else
    python3 "$state/provision.py" --config "$config" --edit "$editor" "$@"
  fi
}
run_installer() (
  root_only
  [[ $# -le 1 ]] || { fail '只接受一个可选版本标签'; return 1; }
  local version=${1:-$script_ref} installer="$state/install.sh" tmp=
  [[ $version =~ ^v[0-9][a-zA-Z0-9._-]*$ ]] || { fail '无效版本标签'; return 1; }
  # Uninstall removes this very menu and its helpers. The running shell still
  # owns these functions, so recover the pinned helper instead of a deleted path.
  trap 'if [[ -n $tmp ]]; then rm -f -- "$tmp/install.sh"; rmdir -- "$tmp"; fi' EXIT
  if [[ ! -f $installer ]]; then
    [[ ! -e $installer && ! -L $installer ]] || { fail '安装脚本路径异常'; return 1; }
    tmp=$(mktemp -d)
    installer="$tmp/install.sh"
    local url="https://raw.githubusercontent.com/shmily2-1/V2bX-2/$script_ref/scripts/install.sh"
    echo '本地安装组件已移除，正在从本仓库固定版本恢复安装器…'
    if command -v curl >/dev/null; then
      curl -fsSL --retry 3 --connect-timeout 15 --max-time 180 --proto '=https' --proto-redir '=https' "$url" -o "$installer"
    elif command -v wget >/dev/null; then
      wget --https-only --timeout=30 --tries=3 -qO "$installer" "$url"
    else
      fail '请先安装 curl 或 wget，以及 CA 证书'; return 1
    fi
    bash -n "$installer"
  fi
  local flags=(--with-systemd --with-manager)
  if [[ -n $root ]]; then flags+=(--destdir "$root"); else flags+=(--install-deps); fi
  bash "$installer" "$version" "${flags[@]}"
)
update() {
  run_installer "$@"
  echo '程序和菜单来自同一个已校验 Release。旧进程未重启，请安排 v2bx restart。'
}
install_node() {
  run_installer "$@"
  echo '========== 首次安装配置指引（支持多个节点） =========='
  echo '程序已安装。下面重新填写面板/节点/证书；确认部署前不会覆盖现有配置。'
  generate
}
ports_help() {
  cat <<'HELP'
Hysteria2 跳跃（独立 hysteria2 / sing）：
  Xboard 需要配套补丁；NodeType=hysteria，面板 version=2。
  例如 port=20000-30000，server_port=8443，hop_interval=30。
  节点需 nftables/iptables、root/CAP_NET_ADMIN 和指定 UDP 范围放行。
  菜单16可显式确认临时放行全部入站端口；正常节点不建议全开放。
  文档：github.com/shmily2-1/V2bX-2/blob/main/docs/HYSTERIA2-PORT-HOPPING.md
HELP
}
# Only these explicit project trees may be recursively removed. Never follow
# a final symlink to a shared certificate directory or cross a mounted tree.
uninstall_tree_path() {
  local logical=$1 parent
  parent=$(resolve "${logical%/*}") || return 1
  printf '%s/%s\n' "$parent" "${logical##*/}"
}
check_uninstall_tree() {
  local target=$1 mounts mount
  [[ ! -L $target ]] || return 0
  mounts=$(findmnt -rn -o TARGET) || { fail '无法检查挂载点，拒绝递归删除'; return 1; }
  while IFS= read -r mount; do
    [[ $mount != "$target" && $mount != "$target/"* ]] || { fail "卸载路径内有挂载点，请先人工处理：$mount"; return 1; }
  done <<< "$mounts"
}
remove_install_backups() {
  local target=$1 candidate suffix
  for candidate in "$target".backup.*; do
    [[ -f $candidate || -L $candidate ]] || continue
    suffix=${candidate#"$target.backup."}
    [[ $suffix =~ ^([0-9]{8}T[0-9]{6}Z\.)?[a-zA-Z0-9]{6}$ ]] || continue
    rm -f -- "$candidate"
  done
}
stop_for_uninstall() {
  local load active
  load=$(systemctl show V2bX.service --property=LoadState --value)
  [[ -n $load ]] || { fail '无法确定服务状态，未卸载'; return 1; }
  if [[ $load == not-found ]]; then
    active=$(systemctl show V2bX.service --property=ActiveState --value)
    case $active in
      inactive|failed) echo '服务单元已不存在，继续清理已识别文件。' ;;
      active|activating|deactivating) systemctl stop V2bX.service ;;
      *) fail '服务运行状态不明确，未卸载'; return 1 ;;
    esac
  else
    systemctl stop V2bX.service
    systemctl disable V2bX.service
  fi
}
uninstall() (
  root_only
  local keep=0 answer confirmation=UNINSTALL-ALL unit path canonical target
  [[ $# == 0 || ( $# == 1 && $1 == --keep-config ) ]] || { fail '仅支持 uninstall [--keep-config]'; return 1; }
  if [[ ${1:-} == --keep-config ]]; then keep=1; confirmation=UNINSTALL; fi
  if ((keep)); then
    echo '移除程序、服务和管理命令；保留配置、证书、ACME账户和数据备份。'
  else
    echo '危险：彻底卸载会删除配置、证书私钥、ACME账户以及本项目备份，无法撤回。'
    echo '删除范围：/etc/V2bX、/usr/local/lib/V2bX-2、/var/backups/V2bX-2、已识别程序/服务/管理命令及安装器备份。'
    echo '只删除项目目录；不追删外部共享证书，不清空全机防火墙，不卸载系统内核。'
    echo '彻底删除证书后重装需要重新签发，频繁卸载重装可能触发CA签发限流。'
  fi
  read -r -p "请输入 $confirmation 确认（其他输入取消）：" answer || answer=
  [[ $answer == "$confirmation" ]] || { echo '已取消'; return 0; }
  find_binary --allow-missing
  unit="$(resolve /etc/systemd/system)/V2bX.service"
  [[ ! -L $unit ]] || { fail '自定义/屏蔽服务链接需人工处理'; return 1; }
  if [[ -f $unit ]]; then
    grep -Eq '^Description=V2bX(-2 node service| Service)$' "$unit" || { fail '自定义服务文件需人工处理，未卸载'; return 1; }
  fi
  command -v findmnt >/dev/null || { fail '缺少 findmnt，请安装 util-linux 后重试'; return 1; }
  local trees=(/usr/local/lib/V2bX-2 /etc/systemd/system/V2bX.service.d /run/systemd/system/V2bX.service.d)
  if ((keep == 0)); then trees+=(/etc/V2bX /var/backups/V2bX-2); fi
  local checked_trees=() commands=()
  for path in "${trees[@]}"; do
    target=$(uninstall_tree_path "$path")
    check_uninstall_tree "$target"
    checked_trees+=("$target")
  done
  # Capture all alias targets before unlinking /usr/bin/V2bX; v2bx may chain to it.
  for path in /usr/bin/V2bX /usr/bin/v2bx /usr/local/bin/v2bx; do
    target=$(uninstall_tree_path "$path")
    canonical=$(realpath -m -- "$target")
    if [[ $canonical == "$self" ]]; then commands+=("$target"); fi
  done
  for path in /etc/systemd/system/multi-user.target.wants/V2bX.service /run/systemd/system/multi-user.target.wants/V2bX.service; do
    target=$(uninstall_tree_path "$path")
    if [[ -L $target && $(realpath -m -- "$target") == "$unit" ]]; then commands+=("$target"); fi
  done
  if [[ -d ${binary%/*} ]]; then
    exec 9>"${binary%/*}/.V2bX.install.lock"
    flock -n 9 || { fail '安装任务正在运行，未卸载'; return 1; }
  fi
  exec 8>"$state/.install.lock"
  flock -n 8 || { fail '管理组件更新正在运行，未卸载'; return 1; }
  if [[ -z $root ]]; then
    exec 7>/run/lock/v2bx-config.lock
    flock -n 7 || { fail '配置部署正在运行，未卸载'; return 1; }
    stop_for_uninstall
  fi
  rm -f -- "$unit" "$binary" "${commands[@]}"
  if ((keep == 0)); then
    remove_install_backups "$binary"
    for path in /usr/bin/V2bX /usr/bin/v2bx /usr/local/bin/v2bx; do
      remove_install_backups "$(uninstall_tree_path "$path")"
    done
  fi
  for target in "${checked_trees[@]}"; do
    if [[ -L $target ]]; then rm -f -- "$target"; else rm -rf --one-file-system -- "$target"; fi
  done
  if [[ -z $root ]]; then systemctl daemon-reload; fi
  rm -f -- "${binary%/*}/.V2bX.install.lock"
  # Never recursively remove a custom or shared executable directory.
  if [[ ${binary%/*} == "$(resolve /usr/local/V2bX)" ]]; then rmdir -- "${binary%/*}" 2>/dev/null || true; fi
  if ((keep)); then
    echo '已卸载程序与服务，配置/证书/数据备份仍保留。'
  else
    echo '已彻底卸载：本项目配置、证书、ACME账户和已识别备份已删除。'
  fi
  echo '当前菜单可直接选择 1 重新安装；退出后请重新执行 GitHub 一键安装命令。'
)
help_text() {
  cat <<'HELP'
V2bX-2 管理命令（兼容原项目，v2bx 始终指向菜单）：
  v2bx                       显示原风格菜单
  v2bx install [v版本]       安装后进入首次多节点配置指引
  v2bx update [v版本]        仅更新程序及菜单，不重新配置/启动
  v2bx update_shell [v版本]   从同一 Release 重装/升级程序及管理组件
  v2bx config|generate       在线预检、证书申请、备份配置并启动验证
  v2bx generate --offline    仅生成配置，不连接面板或启动服务
  v2bx start|stop|restart     启停服务，错误不会被忽略
  v2bx status|log             状态/日志
  v2bx enable|disable        自启设置
  v2bx version|x25519         调用当前真实二进制
  v2bx uninstall             输入 UNINSTALL-ALL 后彻底删除本项目配置/证书/备份
  v2bx uninstall --keep-config 仅卸载程序，输入 UNINSTALL 后保留配置/证书
  v2bx ports                 Hysteria2 跳跃对接说明
  v2bx bbr                   签名发行版源内核/BBR（两次确认，不自动重启）
  v2bx open-ports            全部TCP/UDP入站放行（危险确认、120秒自动撤回）
  v2bx --root /隔离根 ...    验收专用；服务/日志操作被禁止
安装进入首次指引，确认DEPLOY后启动；更新不会自动重启。BBR/全端口功能绝不自动执行。
不执行原版安装器/第三方BBR脚本，不关闭防火墙。
HELP
}
dispatch() {
  local command=${1:-menu}
  [[ $# == 0 ]] || shift
  case $command in
    menu) menu ;;
    install) install_node "$@" ;;
    update|update_shell) update "$@" ;;
    start|stop|restart|enable|disable) control "$command" ;;
    status) status ;;
    config) edit "$@" ;; generate) generate "$@" ;; uninstall) uninstall "$@" ;;
    bbr|open-ports) root_only; host_only; python3 "$state/system-tools.py" "$command" ;;
    log) host_only; journalctl -u V2bX.service -e --no-pager -f ;;
    version|x25519) find_binary; "$binary" "$command" "$@" ;;
    ports) ports_help ;;
    help|-h|--help) help_text ;;
    *) fail '未知命令，使用 v2bx help'; return 2 ;;
  esac
}
menu() {
  local choice action service_state autostart
  while true; do
    if [[ -z $root ]] && command -v systemctl >/dev/null; then
      if systemctl is-active --quiet V2bX.service 2>/dev/null; then service_state='已运行'; else service_state='未运行'; fi
      if systemctl is-enabled --quiet V2bX.service 2>/dev/null; then autostart='是'; else autostart='否'; fi
    else
      service_state='隔离模式'
      autostart='未知'
    fi
    cat <<'MENU'
========== V2bX-2 管理菜单 ==========
0. 修改配置
————————————————
1. 安装 V2bX
2. 更新 V2bX
3. 卸载 V2bX（删除配置/证书/备份）
————————————————
4. 启动 V2bX
5. 停止 V2bX
6. 重启 V2bX
7. 查看 V2bX 状态
8. 查看 V2bX 日志
————————————————
9. 设置 V2bX 开机自启
10. 取消 V2bX 开机自启
————————————————
11. 一键安装 BBR（最新发行版内核）
12. 查看 V2bX 版本
13. 生成 X25519 密钥
14. 升级 V2bX 维护脚本
15. 生成 V2bX 配置文件
16. 放行 VPS 的所有网络端口
17. 退出脚本
MENU
    printf 'V2bX状态: %s\n是否开机自启: %s\n\n' "$service_state" "$autostart"
    read -r -p '请输入选择 [0-17]：' choice || return 0
    case $choice in
      0) action=config ;; 1) action=install ;; 2) action=update ;; 3) action=uninstall ;;
      4) action=start ;; 5) action=stop ;; 6) action=restart ;; 7) action=status ;;
      8) action=log ;; 9) action=enable ;; 10) action=disable ;;
      11) action=bbr ;;
      12) action=version ;; 13) action=x25519 ;; 14) action=update_shell ;;
      15) action=generate ;; 16) action=open-ports ;; 17) return 0 ;; *) echo '无效选项'; continue ;;
    esac
    # A subshell retains errexit for each action. Do not suppress failures via an if/function call.
    set +e
    (set -e; dispatch "$action")
    result=$?
    set -e
    if ((result)); then printf '操作失败（%s），未报告为成功。\n' "$result" >&2; fi
  done
}
dispatch "$@"
