# V2bX-2

基于 [wyx2685/V2bX](https://github.com/wyx2685/V2bX) 的独立维护版本。上游基线：`dev_new` / `71277de69efbbc86c23ad8ae02b68efd174e5756`。

本仓库：**https://github.com/shmily2-1/V2bX-2**。保留 `V2bX` 可执行文件名称和原配置结构，升级三套节点内核及相关协议库。

> 当前为升级验收版，不代表所有生产配置均已验证。先在测试节点验证客户端、流量上报、限速、证书和断线行为，再切换生产节点。不要使用旧仓库的一键脚本更新本项目，否则可能覆盖回上游版本。

## 内核版本（2026-09-19 核实）

| 内核 | 上游项目原版本 | 本仓库版本 | 维护方式 |
|---|---|---|---|
| Xray | 25.12.2 + 作者 fork | **26.11.9** / Go module `v1.260327.1-0.20261109052709-676eaf66f8f0` | 官方预发布版本，移除旧 fork |
| sing-box | 1.13.0-alpha.5 定制快照 | **1.15.0-alpha.13 + 兼容补丁** | 官方预发布源码内嵌，保留动态用户管理 |
| Hysteria 2 core/extras | 2.6.4 | **2.13.0** | 官方模块 |
| Go | 1.25.0 | **1.27.1** | 本地与 CI 统一 |

Xray `26.9.9` 等更新条目在核实时被官方标记为 prerelease，本分支不会静默切换到预发布内核。内核版本以 `V2bX version` 实际输出为准。

## 协议与核心选择

| 核心 Type | 协议 |
|---|---|
| `xray` | VMess、VLESS、Trojan、Shadowsocks |
| `sing` | VMess、VLESS、Trojan、Shadowsocks、TUIC、AnyTLS、Hysteria 1、Hysteria 2 |
| `hysteria2` | 独立官方 Hysteria 2 |

Hysteria 1、TUIC、AnyTLS、VMess/VLESS、Shadowsocks 不全是独立可执行“内核”，它们随所选主内核及协议库更新。面板仍使用上游 V2board/Xboard 兼容 API；本次没有修改线上面板或替任何节点生成用户。

## 构建

### Hysteria2 端口跳跃 / cedar2025 Xboard

`hysteria2` 和 `sing` 两套内核支持 Linux UDP 端口范围转发，并识别 Xboard 的 `hysteria + version=2`。范围由节点 API 的 `ports` 下发；需要 root/NET_ADMIN 及 nftables 或 iptables。

**Xboard 必须应用配套后端补丁**，不能只改节点二进制。设置、补丁、示例和安全边界见 [Hysteria2 端口跳跃对接说明](docs/HYSTERIA2-PORT-HOPPING.md)。不影响未配置范围的单端口节点；非 Linux 不会静默忽略范围。

### 源码构建

```bash
git clone https://github.com/shmily2-1/V2bX-2.git
cd V2bX-2
export GOEXPERIMENT=jsonv2
export CGO_ENABLED=0
TAGS='sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor'
go build -trimpath -tags "$TAGS" -ldflags '-s -w' -o V2bX .
./V2bX version
```

可通过 `xray`、`sing`、`hysteria2` 构建标签选择内核。使用 `sing` 的 QUIC 协议必须保留 `with_quic`。源码内已包含 sing-box 兼容层，不需要另拉子模块，也不需要作者 fork 在线可用。

## 安装 / 更新

### Linux 一键安装（兼容原 V2bX 命令）

支持 **amd64 / arm64**，目标是常见 Debian / Ubuntu / RHEL 系 **systemd** 服务器；一键入口不会在 OpenRC、SysVinit、runit 或未运行 systemd 的容器中继续安装。先备份现有配置并阅读 [升级说明](docs/UPGRADE.md)。当前固定安装 `v0.1.0-core-upgrade.11` **验收版 / prerelease**，内核按官方预发布版本更新。

与原项目命令格式一致：

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
```

这里只兼容原项目“一条 `wget` + `bash`”的调用形状；脚本地址、版本解析、下载源和更新逻辑全部属于本仓库实现，不会回调或覆盖回 `wyx2685/V2bX-script`。

生产环境建议固定已验收标签，避免 `main` 变化：

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.11/install.sh && bash install.sh
```

- 从本仓库 Release 下载对应架构，校验 SHA256，原子替换程序并备份旧文件。下载/校验失败不会安装。
- 兼容菜单的一键新装默认程序路径为 `/usr/local/V2bX/V2bX`；更新优先使用现有 `V2bX.service` 的实际程序路径，并兼容已有 `/usr/local/bin/V2bX` 和符号链接。路径冲突会停止，不会假装更新成功。
- 安装器会先执行适配后的 `scripts/install.sh`，仅在缺少工具时安装必要系统包，不做整机升级；**不安装/启用防火墙服务，不改端口规则**。
- 首次安装会把兼容原项目的 `V2bX` 管理命令装到 `/usr/bin/V2bX`，并创建 `v2bx` 别名，提供 `start|stop|restart|status|enable|disable|log|update|generate|config|version|x25519|uninstall|ports` 等菜单/命令。**不会覆盖配置/证书，不自动启动、重启或设置开机启动。**

首次配置只填写 **核心、面板地址、面板密钥（不回显）、节点ID**。之后自动识别协议、读取域名与端口、复用/申请HTTP证书、备份并写入配置、启动和设置自启；不再询问HTTP确认、协议、证书方式、邮箱、Y/N或DEPLOY。缺少必要域名或部署失败时返回真实错误，保留回滚；REALITY/无TLS不申请公共证书。

菜单1 / `v2bx install` 安装后进入四项自动配置指引；菜单15 / `v2bx generate` 也使用该流程。每次配置一个节点并合并到现有配置，保留其他节点和原有调优参数。需要手动协议、DNS证书或原多节点Y/N向导时使用 `v2bx generate --advanced`。菜单2只更新组件。菜单3直接彻底删除本项目数据；保留数据用 `v2bx uninstall --keep-config`。

HTTP-01需要TCP80且被占用时，自动停止并恢复可识别的托管服务；独立进程使用pidfd执行TERM/KILL，无法自动恢复的独立进程会有服务中断风险。`--check-only`不释放端口。同一节点复用原证书及ACME配置；自动续签仍需满足所选证书方式的网络条件。

只要离线生成配置：`v2bx generate --offline`。`--root`始终离线，禁止宿主服务操作。菜单编号与原项目一致，8日志、9自启、10取消自启、11BBR、16全端口放行。菜单操作直接执行；BBR只使用发行版软件源且不自动重启，全开放保留NAT并提供手动撤回状态。详见[在线配置及系统维护](docs/ONLINE-PROVISION.md)。

OpenRC、SysVinit、runit 或未运行 systemd 的容器不在一键入口支持范围内；脚本会在下载前拒绝继续，不会自动改造成其它 init 系统。需要二进制-only 部署时请先审查 Release 包并手动托管，不要执行依赖 `systemctl` 的管理命令。指定版本、升级/回滚步骤、服务和配置说明见 [安装指南](docs/INSTALL.md)。**Hysteria2 跳跃仍须配套 Xboard 补丁及 UDP 放行，安装脚本不会代改面板。**

## 验证与维护

下面的命令针对源码仓库 checkout；Release ZIP 只包含运行所需的资料和管理 bundle，不保证包含完整测试脚本目录。

```bash
GOEXPERIMENT=jsonv2 go test -tags "$TAGS" -count=1 -timeout 180s ./...
python3 scripts/check-sing-patches.py
```

- 本地回环生命周期覆盖三套内核、13 个核心/协议组合。
- 真实回环数据面覆盖 Xray/VLESS、sing/VLESS、sing/Hysteria2、独立 Hysteria2：认证、TCP echo、双向流量计数、删除用户后的新连接拒绝。
- 这些不等同于生产面板接入、所有协议/客户端互通、REALITY、UDP 转发、限速精度及长时间并发压测。
- 上游需要真实面板或 ACME/DNS 的旧测试已隔离在 `integration` 标签，普通测试不联系生产服务。
- 推送 main/PR 后自动测试并构建 Linux amd64/arm64、Windows amd64、macOS arm64。推送 `v*` 标签后仅在测试和全部构建通过时发布 **prerelease**。
- Docker 镜像发布只允许人工触发，目标为 `ghcr.io/shmily2-1/v2bx-2`。

后续升级方法、行为变化及验证边界见 [升级说明](docs/UPGRADE.md)。sing-box 原始校验值和补丁清单见 `third_party/sing-box-patches.json`。

本次本地验证的具体结果和未覆盖范围见 [验收记录](docs/VALIDATION.md)。

## 来源与许可证

保留上游提交历史与根目录 `LICENSE`；内嵌 sing-box 保留其 `LICENSE` 和源码，动态用户兼容代码源自 wyx2685/sing-box_mod 并作适配。构建/分发请同时保留各组成部分的许可证及源码义务。感谢 V2bX、Xray、sing-box、Hysteria 及相关上游贡献者。
