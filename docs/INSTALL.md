# GitHub 一键安装与更新

## 适用范围

- Linux amd64 / arm64，使用 Bash；一键入口只支持正在运行的 systemd。OpenRC、SysVinit、runit 和未运行 systemd 的容器会在下载前被拒绝。
- 当前默认：`v0.1.0-core-upgrade.2`，明确为验收版 / prerelease。固定标签使脚本和下载版本对应，不使用会排除 prerelease 的 GitHub `/releases/latest`。
- 从根 README 复制一键命令，以 root 执行。命令先完整下载到随机临时文件，成功后才执行，不使用 `curl | bash`。执行远程脚本前应人工查看内容。
- 初始下载需要 curl 和 CA 证书。Debian/Ubuntu 可先运行 `apt-get update && apt-get install -y curl ca-certificates`；RHEL 系使用对应的 dnf/yum。
- 脚本可用 `--install-deps` 补齐 curl、unzip、coreutils、util-linux 所提供的工具；不会整机升级。其它发行版自行安装依赖。
- 这里只兼容原项目“一条 `wget` + `bash`”的调用形状；脚本地址、版本解析、下载源和更新逻辑全部属于本仓库实现，不会回调 `wyx2685/V2bX-script`。

## 原项目兼容的一键命令

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
```

固定版本安装：

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.2/install.sh && bash install.sh
```

如需把版本参数也显式写入审计记录，可在查看脚本后执行 `bash install.sh v0.1.0-core-upgrade.2`；该标签脚本默认值已经固定为同一版本。

## 可审核的分步安装

先查看脚本，再执行固定版本安装：

```bash
less install.sh
bash install.sh v0.1.0-core-upgrade.2 --non-interactive
```

下载文件名 `V2bX-linux-amd64.zip` 或 `V2bX-linux-arm64.zip`，使用同一 Release 的 `.zip.sha256` 校验。只从 HTTPS GitHub 下载，不回退到未知镜像。SHA256 能检测损坏/不匹配，不是独立于 GitHub 仓库的签名。标签、脚本和 Release 都属于同一信任边界。

## 首次配置

新装默认程序 `/usr/local/bin/V2bX`。兼容原项目的 bootstrap 还安装：

- `/etc/systemd/system/V2bX.service`（只有无现有本地/运行时/发行版服务时才创建）；
- `/etc/V2bX/config.json.example`（权限 0600，Hysteria2 + Xboard 示例，不覆盖已有同名文件）；
- `/usr/bin/V2bX` 和 `/usr/local/bin/v2bx`（管理菜单，兼容 `start/stop/restart/status/enable/disable/log/update/generate/config/version/x25519/uninstall/ports`）；
- `/usr/local/lib/V2bX-2/`（与已校验 ZIP 同源的管理脚本和运行时资源）；
- **不会直接生成生产 `config.json`，不会申请证书或启动节点。**

仅新节点执行以下初始化，已存在配置时第一步会跳过：

```bash
test -e /etc/V2bX/config.json || install -m 0600 /etc/V2bX/config.json.example /etc/V2bX/config.json
${EDITOR:-vi} /etc/V2bX/config.json
```

填写 `ApiHost`、`ApiKey`、`NodeID`、证书域名、证书/私钥路径。示例 `NodeType` 为 `hysteria`，面板协议版本设置为 2。其它协议按仓库配置说明调整 `Cores` 和 `Nodes`，不要直接使用示例占位值启动。

已确认面板配置、证书、监听端口和客户端兼容性后：

```bash
systemctl enable --now V2bX
systemctl status V2bX --no-pager
journalctl -u V2bX -n 100 --no-pager
```

创建的服务以 root 运行，便于端口绑定和跳跃 NAT 管理；不自动对现有非 root 服务提权。`Restart=on-failure` 不替代配置正确性或真实数据面验收。

### 非 systemd 环境

OpenRC、SysVinit、runit 和未运行 systemd 的容器不在本一键入口支持范围内；顶层 `install.sh` 会在下载前拒绝继续，不会注册其它服务单元或自动开机启动。需要 binary-only 部署时请先审查 Release 包，再由已有 supervisor 手动托管；不要执行依赖 `systemctl` 的 `enable/start/stop/restart/log` 管理命令。

## 更新与回滚

1. 先保存 `systemctl cat V2bX`、实际二进制 SHA256，并备份 `/etc/V2bX`（含私钥），备份应只允许管理员访问。
2. 再运行安装命令。脚本会读取现有服务 `ExecStart`，或识别 `/usr/local/V2bX/V2bX`。有两个不同安装且无可用服务路径时停止，人工确认后用 `--install-dir /实际目录`。自定义包装器不做猜测。
3. 新二进制先通过 `version` 启动检查，再在同目录原子替换；旧程序保存在 `V2bX.backup.<UTC时间>.<随机后缀>`。原符号链接保留。已有配置、示例、systemd 文件不改。
4. **运行中的老进程不会自动切换**。在维护窗口人工执行 `systemctl restart V2bX`，检查日志、用户、流量及真实客户端。
5. 若失败，先停止节点，将明确选定的备份复制为同目录临时文件，再原子替换实际 `ExecStart` 程序，恢复必要配置后启动。不要盲目选择通配符匹配出的「最后一个」备份。

仅装程序：`sudo bash install.sh v0.1.0-core-upgrade.2`。另一个版本只有其 Release 和对应架构资产实际存在时才可指定。`V2bX update` 使用本地已校验适配安装器，不会运行 wyx2685 原版安装器；以后继续使用本仓库的一键命令或本地管理命令更新。

## Hysteria2 端口跳跃

脚本不会安装/启用防火墙服务、操作安全组或修改面板。必须另外完成：

- 应用 `docs/patches/Xboard-hysteria2-port-hopping.patch`，详见 `HYSTERIA2-PORT-HOPPING.md`；
- 面板设置如 `port=20000-30000`、`server_port=8443`、`version=2`、`hop_interval=30`；
- 节点准备 nftables 或 iptables，以及 root / CAP_NET_ADMIN；
- 按业务需求放行相应 UDP 范围和监听端口的云安全组/宿主防火墙。

不要为了跳跃功能直接 flush 全机防火墙或开启一个会覆盖现有规则的新防火墙管理服务。

## 隔离验证

以下命令仅适用于源码仓库 checkout，不保证在 Release ZIP 中存在完整的 `scripts/` 测试目录。

```bash
bash -n scripts/install.sh
shellcheck scripts/install.sh install.sh V2bX.sh initconfig.sh
python3 scripts/test-install.py
python3 scripts/test-bootstrap.py
```

离线测试使用伪下载和伪服务查询，但 ZIP 解包、SHA256、备份、软链接和原子替换使用真实系统工具。`--destdir /tmp/独立目录`（路径请用 ASCII）用于打包/验收，可无 root 执行；所有文件放入指定隔离根，不调用宿主 systemctl，不允许 `--install-deps`。这一选项不是生产配置目录选项。
