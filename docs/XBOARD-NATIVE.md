# Xboard 原生界面适配

面板源码位于 `shmily2-1/Xboard`。扩展配置使用原 Xboard 节点列表及编辑弹窗；机器服务和 Peer 使用原服务器详情；用户下载配置位于原仪表盘的订阅卡片。使用原有账号、会话、权限组与订阅。

节点端仍使用原 UniProxy 配置、用户和计量接口，HTTP/Mixed 选择 sing，MASQUE 与 VLESS/XDRIVE 选择 Xray。新增功能不要求更换既有代理节点配置。

机器服务同步组件在安装包中提供，一键安装后运行：

```sh
v2bx panel-agent --config /etc/V2bX/xboard-agent.json
```

单次检查：

```sh
v2bx panel-agent --config /etc/V2bX/xboard-agent.json --once
```

配置保存在节点私有目录，字段为 `panel`、`machine_id`、`token`、`state_dir`、`v2bx`、`v2bx_config`、`interval`，独立后端另配 `backend_profiles` 和 `metrics_ports`。`v2bx` 必须指向实际二进制，例如 `/usr/local/V2bX/V2bX`；不要指向管理菜单别名。V2bX 主配置的 `LocalServiceDir` 必须与同步组件一致。

同步优先使用 `/api/v2/server/machine/services/pull`、`ack`、`meter`，旧面板返回 404 时才使用兼容接口。原有已安装同步组件可以继续连接新面板。

组件不额外创建代理用户、不重复读取核心原子计数器；配置写入后由运行中的 V2bX 应用，面板根据实际应用修订确认状态。代理独立运行，可由现有进程管理器托管，不自动新增系统服务、设置 NAT 或下载外部后端。

旧版管理器显式升级到 `.18` 后若缺少 `panel-agent` 文件，重新执行本仓库的一键安装入口即可补齐同版本管理组件。退回旧版本前先停用同步任务并保留服务配置、密钥和待报状态。
