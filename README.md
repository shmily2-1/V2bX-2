# V2bX-2

基于 [wyx2685/V2bX](https://github.com/wyx2685/V2bX) 的独立维护版本。上游基线：`dev_new` / `71277de69efbbc86c23ad8ae02b68efd174e5756`。

本仓库：**https://github.com/shmily2-1/V2bX-2**。保留 `V2bX` 可执行文件名称和原配置结构，升级三套节点内核及相关协议库。

> 当前为升级验收版，不代表所有生产配置均已验证。先在测试节点验证客户端、流量上报、限速、证书和断线行为，再切换生产节点。不要使用旧仓库的一键脚本更新本项目，否则可能覆盖回上游版本。

## 内核版本（2026-09-19 核实）

| 内核 | 上游项目原版本 | 本仓库版本 | 维护方式 |
|---|---|---|---|
| Xray | 25.12.2 + 作者 fork | **26.3.27** / Go module `v1.260327.0` | 官方标记的最新非预发布版本，移除旧 fork |
| sing-box | 1.13.0-alpha.5 定制快照 | **1.14.1 + 兼容补丁** | 官方源码内嵌，保留动态用户管理 |
| Hysteria 2 core/extras | 2.6.4 | **2.12.3** | 官方模块 |
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

### Linux 一键安装（root 执行）

支持 **amd64 / arm64**，常见 Debian / Ubuntu / RHEL 系 systemd 服务器。先备份现有配置并阅读 [升级说明](docs/UPGRADE.md)。当前固定安装 `v0.1.0-core-upgrade.1` **验收版 / prerelease**，不是未经核实的「最新版」。需要能连接 GitHub，并预装 Bash、curl 和 CA 证书。

```bash
(set -e; f=$(mktemp); trap 'rm -f -- "$f"' EXIT; curl -fsSL --retry 3 --proto '=https' --proto-redir '=https' https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.1/scripts/install.sh -o "$f"; bash "$f" --install-deps --with-systemd)
```

- 从本仓库 Release 下载对应架构，校验 SHA256，原子替换程序并备份旧文件。下载/校验失败不会安装。
- 新装默认 `/usr/local/bin/V2bX`；更新优先使用现有 `V2bX.service` 的实际程序路径，兼容旧 `/usr/local/V2bX/V2bX` 和符号链接。路径冲突会停止，不会假装更新成功。
- `--install-deps` 仅在缺少工具时安装必要系统包，不做整机升级；**不安装/启用防火墙服务，不改端口规则**。
- `--with-systemd` 仅在不存在服务时创建 `V2bX.service`，提供 `/etc/V2bX/config.json.example`。**不覆盖配置/证书/已有服务，不自动启动、重启或设置开机启动。**

首次安装后，参考示例填入面板地址、密钥、节点 ID 和证书路径，保存为 `/etc/V2bX/config.json`，验证配置后由你执行 `systemctl enable --now V2bX`。老节点更新后需自行安排 `systemctl restart V2bX` 才生效。

非 systemd 环境去掉 `--with-systemd`；仅安装程序可同时去掉 `--install-deps` 并自行准备依赖。指定版本、升级/回滚步骤、服务和配置说明见 [安装指南](docs/INSTALL.md)。**Hysteria2 跳跃仍须配套 Xboard 补丁及 UDP 放行，安装脚本不会代改面板。**

## 验证与维护

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
