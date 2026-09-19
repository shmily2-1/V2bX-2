#!/usr/bin/env bash
# Original-style V2bX management menu, adapted from wyx2685/V2bX-script (MPL-2.0).
# Installed as /usr/bin/V2bX and /usr/local/bin/v2bx. No upstream downloads.
set -euo pipefail
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
  [[ $binary != "$self" && -x $binary ]] || { fail '程序不存在或指向管理脚本'; return 1; }
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
    python3 "$state/configure.py" --config "$config" --root "$root"
  else
    bash "$state/initconfig.sh"
  fi
}
edit() {
  root_only
  # Edit a private temporary copy; do not let the server's file watcher read half a JSON.
  local editor=${EDITOR:-vi}
  if [[ -n $root ]]; then
    python3 "$state/configure.py" --config "$config" --root "$root" --edit "$editor"
  else
    python3 "$state/configure.py" --config "$config" --edit "$editor"
  fi
}
update() {
  root_only
  [[ $# -le 1 ]] || { fail '只接受一个可选版本标签'; return 1; }
  local flags=(--with-systemd --with-manager)
  if [[ -n $root ]]; then flags+=(--destdir "$root"); else flags+=(--install-deps); fi
  bash "$state/install.sh" "$@" "${flags[@]}"
  echo '程序和菜单来自同一个已校验 Release。旧进程未重启，请安排 v2bx restart。'
}
ports_help() {
  cat <<'HELP'
Hysteria2 跳跃（独立 hysteria2 / sing）：
  Xboard 需要配套补丁；NodeType=hysteria，面板 version=2。
  例如 port=20000-30000，server_port=8443，hop_interval=30。
  节点需 nftables/iptables、root/CAP_NET_ADMIN 和指定 UDP 范围放行。
  不提供“关闭防火墙/放开全部端口”，不 flush 规则，不修改面板。
  文档：github.com/shmily2-1/V2bX-2/blob/main/docs/HYSTERIA2-PORT-HOPPING.md
HELP
}
uninstall() {
  root_only
  echo '仅移除本次安装记录中的程序、已识别服务和管理命令；配置/证书/备份保留。'
  local answer unit path canonical
  read -r -p '请输入 UNINSTALL 确认（其他输入取消）：' answer || answer=
  [[ $answer == UNINSTALL ]] || { echo '已取消'; return 0; }
  find_binary
  unit="$(resolve /etc/systemd/system)/V2bX.service"
  if [[ -L $unit ]]; then fail '自定义/屏蔽服务链接需人工处理'; return 1; fi
  if [[ -f $unit ]]; then
    grep -Eq '^Description=V2bX(-2 node service| Service)$' "$unit" || { fail '自定义服务文件需人工处理，未卸载'; return 1; }
  fi
  if [[ -z $root ]]; then
    systemctl stop V2bX.service
    systemctl disable V2bX.service
  fi
  rm -f -- "$unit" "$binary"
  if [[ -z $root ]]; then systemctl daemon-reload; fi
  for path in /usr/bin/V2bX /usr/bin/v2bx /usr/local/bin/v2bx; do
    resolve "${path%/*}" >/dev/null
    canonical=$(realpath -m -- "$root$path")
    if [[ $canonical == "$self" ]]; then rm -f -- "$root$path"; fi
  done
  for path in V2bX.sh initconfig.sh configure.py install.sh bootstrap.sh binary-path; do rm -f -- "$state/$path"; done
  echo '已卸载已识别程序与服务，/etc/V2bX 和所有备份仍保留。'
}
help_text() {
  cat <<'HELP'
V2bX-2 管理命令（兼容原项目，v2bx 始终指向菜单）：
  v2bx                       显示原风格菜单
  v2bx install|update [v版本] 安装程序及菜单；默认本菜单对应验收版
  v2bx update_shell [v版本]   从同一 Release 重装/升级程序及管理组件
  v2bx config|generate       安全编辑/多节点配置向导（会提示覆盖及热重载风险）
  v2bx start|stop|restart     启停服务，错误不会被忽略
  v2bx status|log             状态/日志
  v2bx enable|disable        自启设置
  v2bx version|x25519         调用当前真实二进制
  v2bx uninstall             确认后卸载，保留配置/证书/备份
  v2bx ports                 Hysteria2 跳跃对接说明
  v2bx --root /隔离根 ...    验收专用；服务/日志操作被禁止
普通安装不会自动重启；不执行原版安装器/第三方 BBR/统计上报，不关闭防火墙。
HELP
}
dispatch() {
  local command=${1:-menu}
  [[ $# == 0 ]] || shift
  case $command in
    menu) menu ;;
    install|update|update_shell) update "$@" ;;
    start|stop|restart|enable|disable) control "$command" ;;
    status) status ;;
    config) edit ;; generate) generate ;; uninstall) uninstall ;;
    log) host_only; journalctl -u V2bX.service -e --no-pager -f ;;
    version|x25519) find_binary; "$binary" "$command" "$@" ;;
    ports) ports_help ;;
    help|-h|--help) help_text ;;
    *) fail '未知命令，使用 v2bx help'; return 2 ;;
  esac
}
menu() {
  local choice action
  while true; do
    cat <<'MENU'
========== V2bX-2 管理菜单 ==========
0. 编辑配置            1. 安装/重装
2. 更新程序和菜单      3. 卸载（保留配置）
4. 启动                5. 停止
6. 重启                7. 状态
8. 开机自启            9. 取消自启
10. 日志               11. BBR（不执行第三方脚本）
12. 版本               13. X25519 密钥
14. 更新管理组件       15. 生成多节点配置
16. Hysteria2 跳跃说明  17. 退出
MENU
    read -r -p '请输入 [0-17]：' choice || return 0
    case $choice in
      0) action=config ;; 1) action=install ;; 2) action=update ;; 3) action=uninstall ;;
      4) action=start ;; 5) action=stop ;; 6) action=restart ;; 7) action=status ;;
      8) action=enable ;; 9) action=disable ;; 10) action=log ;;
      11) echo '未执行任何 BBR/内核/网络调优；如需调优请单独审查方案。'; continue ;;
      12) action=version ;; 13) action=x25519 ;; 14) action=update_shell ;;
      15) action=generate ;; 16) action=ports ;; 17) return 0 ;; *) echo '无效选项'; continue ;;
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
