# v0.1.0-core-upgrade.2：原项目兼容安装器 / Hysteria2 跳跃验收版

这是 **prerelease / 验收版**，请先在备用节点验证，不代表所有生产配置和客户端已兼容。当前标签为 `v0.1.0-core-upgrade.2`。

- Xray 26.3.27、sing-box 1.14.1 兼容层、Hysteria2 core/extras 2.12.3。
- 独立 Hysteria2 与 sing 两内核支持 Linux 端口跳跃，配套 cedar2025/Xboard 后端补丁在源码及包内 `docs/patches/`。
- Linux amd64/arm64 一键安装，SHA256 校验，旧二进制备份，保护原配置和实际服务路径。
- 保留原项目“一条 wget + bash”调用形状，但脚本、下载源和更新逻辑全部来自 `shmily2-1/V2bX-2`，不会调用旧仓库安装器。
- 可创建未启动的 systemd 服务、原风格 `V2bX/v2bx` 管理菜单及 Hysteria2/Xboard 配置示例；不会自动启动、重启、设置开机启动或改防火墙。
- 另提供 Windows amd64、macOS arm64 便携包；Linux 安装脚本不适用于它们。

## 一键安装（root，Linux amd64/arm64 + systemd）

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
```

生产环境建议固定当前验收标签：

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.2/install.sh && bash install.sh
```

首次安装后编辑 `/etc/V2bX/config.json.example` 中的占位参数，保存为 `/etc/V2bX/config.json`，确认配置和证书后才执行 `systemctl enable --now V2bX` 或 `v2bx enable && v2bx start`。已有节点需要维护者自行安排重启；安装器不会自动重启。

## 支持边界

- systemd 是自动创建服务单元、`enable/start/stop/restart/log` 管理命令的唯一集成目标；顶层一键入口会在非 systemd 主机下载前拒绝继续。
- OpenRC、SysVinit、runit 和未运行 systemd 的容器不在一键入口支持范围内；binary-only 部署需自行审查 Release 包并由已有 supervisor 托管。
- Hysteria2 端口跳跃仍需要 cedar2025/Xboard 配套补丁、nftables/iptables、root 或 `CAP_NET_ADMIN`，以及云安全组/宿主防火墙的 UDP 放行；本版本不会 flush 全机规则或修改面板。

阅读包内 `docs/INSTALL.md`、`docs/UPGRADE.md`、`docs/HYSTERIA2-PORT-HOPPING.md` 和 `docs/VALIDATION.md`。SHA256 与资产同源，不能替代独立签名。源码以本 Release 标签/提交为准，内嵌 sing-box 的许可证和对应源码保留。源码级测试命令只保证在仓库 checkout 中可用。
