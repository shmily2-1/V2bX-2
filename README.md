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

1. 从本仓库 Releases 或已通过 CI 的 Actions artifacts 下载对应平台包。
2. 校验同目录 `.zip.sha256`，备份原二进制、配置及 systemd 文件。
3. 检查 `docs/UPGRADE.md` 的兼容性变化，再在测试节点运行。

Linux 可下载并人工检查本仓库 `scripts/install.sh` 后执行：

```bash
sudo bash install.sh v0.1.0-core-upgrade.1
```

该示例版本只有在对应 Release 发布后才可安装。脚本只替换 `/usr/local/bin/V2bX`，备份该位置的原二进制，**不会改配置、申请证书、创建服务或自动重启节点**。旧安装器可能使用 `/usr/local/V2bX/V2bX`，请先检查实际 `ExecStart`。

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
