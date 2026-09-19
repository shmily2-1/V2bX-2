# 首个内核升级 / Hysteria2 跳跃验收版

这是 **prerelease / 验收版**，请先在备用节点验证，不代表所有生产配置和客户端已兼容。

- Xray 26.3.27、sing-box 1.14.1 兼容层、Hysteria2 core/extras 2.12.3。
- 独立 Hysteria2 与 sing 两内核支持 Linux 端口跳跃，配套 cedar2025/Xboard 后端补丁在源码及包内 `docs/patches/`。
- Linux amd64/arm64 一键安装，SHA256 校验，旧二进制备份，保护原配置和实际服务路径。
- 可创建未启动的 systemd 服务及 Hysteria2/Xboard 配置示例；不会自动启动、重启或改防火墙。
- 另提供 Windows amd64、macOS arm64 便携包；Linux 安装脚本不适用于它们。

## 一键安装（root，Linux amd64/arm64 + systemd）

```bash
(set -e; f=$(mktemp); trap 'rm -f -- "$f"' EXIT; curl -fsSL --retry 3 --proto '=https' --proto-redir '=https' https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.1/scripts/install.sh -o "$f"; bash "$f" --install-deps --with-systemd)
```

首次安装后编辑 `/etc/V2bX/config.json.example` 中的占位参数，保存为 `/etc/V2bX/config.json`，确认配置和证书后才执行 `systemctl enable --now V2bX`。已有节点需要维护者自行安排重启。

阅读包内 `docs/INSTALL.md`、`docs/UPGRADE.md`、`docs/HYSTERIA2-PORT-HOPPING.md` 和 `docs/VALIDATION.md`。SHA256 与资产同源，不能替代独立签名。源码以本 Release 标签/提交为准，内嵌 sing-box 的许可证和对应源码保留。
