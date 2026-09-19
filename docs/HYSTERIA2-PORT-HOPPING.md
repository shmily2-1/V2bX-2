# Hysteria2 端口跳跃：cedar2025/Xboard 对接

## 支持范围与工作方式

支持 `hysteria2` 独立内核和 `sing` 内核的 Hysteria2 节点；Xray、Hysteria1 不在本功能范围。

```text
客户端在 UDP 20000-30000 内跳跃
             ↓
节点 Linux nftables / iptables NAT（仅本机目的地址）
             ↓
单个 Hysteria2 UDP 8443 监听 → 面板用户认证 / 转发 / 流量统计
```

**服务端不轮流更换监听端口，也不会绑定几千个 socket。** `hop_interval` 是客户端跳跃间隔（秒），服务端同时接收整个范围。0/null 表示客户端默认值；指定值至少 5 秒。

## 1. 先补齐 Xboard 节点配置 API

对接基线：`cedar2025/Xboard` commit `4f48e61a2cbc6db5338872b6bdb45ef954ec1256`。本仓库提供 `docs/patches/Xboard-hysteria2-port-hopping.patch`，没有替你修改或推送第三方仓库。

该基线的订阅已支持 Hysteria2 动态端口，但 `/api/v1/server/UniProxy/config` 的 `hysteria/version=2` 分支没有下发 `ports`、`hop_interval`。补丁读取当前节点自身的 **port** 范围并输出这两个字段，同时保留 **server_port** 的固定监听端口。V2 API 复用同一配置构建方法。

在面板源码根目录操作，先备份并审查差异；下面的 `PATCH` 改为下载的补丁绝对路径：

```bash
PATCH=/path/to/Xboard-hysteria2-port-hopping.patch
git status --short
git apply --check "$PATCH"
git apply "$PATCH"
php -l app/Services/ServerService.php
php vendor/bin/phpunit --bootstrap vendor/autoload.php tests/Unit/Hysteria2PortHoppingTest.php
```

`--check` 不通过时不要强制覆盖；应根据你的面板版本人工合并。补丁只修改配置构建并添加无数据库单元测试，不改表结构、数据库内容或前端。按自己的部署方式重载 PHP/Octane 进程，使代码生效；不要不加判断地对生产环境运行重装脚本。

### Xboard 面板设置示例

| 设置 | 示例 | 含义 |
|---|---|---|
| 节点类型 | Hysteria | Xboard 内部名称 |
| 协议版本 | 2 | 不是 Hysteria1 |
| 连接端口 `port` | `20000-30000` | 客户端订阅使用的公网范围 |
| 后端端口 `server_port` | `8443` | 节点只监听这个 UDP 端口 |
| `protocol_settings.hop_interval` | `30` | 客户端每 30 秒跳跃 |
| TLS / 混淆 | 按实际证书配置 | 两端必须匹配 |

配套补丁遵循 Xboard 已有订阅实现，**面板端只支持一个 `起始-结束` 范围，不支持逗号列表**；无效格式明确报错，避免订阅和节点各自解释不同。V2bX 的通用 `ports` 解析器可以接受 `20000-21000,22000`，但这不等于当前 Xboard 订阅支持它。单端口节点下发 `ports:null`，不触发防火墙操作。

打补丁后的节点 API 核心字段应类似（示意，不是生产响应）：

```json
{
  "protocol": "hysteria",
  "version": 2,
  "server_port": 8443,
  "ports": "20000-30000",
  "hop_interval": 30,
  "base_config": { "push_interval": 60, "pull_interval": 60 }
}
```

`NodeType: hysteria` 的 API 查询仍保持 `hysteria`，V2bX 内部会根据 `version:2` 自动选择 Hysteria2 协议；旧面板 `NodeType: hysteria2` 仍兼容。不要将 `server_port` 改成字符串范围。

## 2. V2bX-2 节点配置

完整示例：`example/hysteria2-xboard.json`。替换面板地址、密钥、NodeID、证书路径后在备用节点验收。使用 sing 时把 `Cores[].Type` 和 `Nodes[].Core` 都改为 `sing`。端口范围来自面板 API，不是把 `ports` 加到本地 Nodes 配置就会生效。

本功能要求：

- Linux；有 `nft` 或 `iptables`，双栈 iptables 模式还需要 `ip6tables`。
- root 或足够的 `CAP_NET_ADMIN`；仅 `CAP_NET_BIND_SERVICE` 不够。
- 默认优先 nftables；没有 nft 可执行文件才选 iptables。nft 存在但运行失败时明确失败，不偷偷切换另一套防火墙。
- 可通过服务环境变量 `V2BX_FIREWALL_BACKEND=nftables` 或 `iptables` 指定；不设置或 `auto` 为自动选择。
- 非 Linux 收到非空 `ports` 时明确拒绝启动该节点，不会静默成为单端口。

`"ListenIP":"0.0.0.0"` 仅 IPv4；`"ListenIP":"::"` 为双栈 wildcard；具体 IPv4/IPv6 地址只匹配该地址。独立内核的 `::` 在本分支统一为 Go 双栈监听语义，与 sing 对齐；内核禁用 IPv6 时应使用具体 IPv4 地址并单独验收。

## 3. 防火墙、安全组与容器

- 本程序仅创建自己的 NAT 表/链：nft `v2bx_hop_*`，iptables `V2BX-HOP-*`。不 flush 全局规则，不修改 INPUT/FORWARD 策略，不自动增加 ACCEPT。
- 必须在云安全组/上游防火墙放行 **UDP 跳跃范围**。主机 INPUT 过滤看到 NAT 后的端口，通常还需要允许 **UDP server_port**。TCP 范围放行不能代替 UDP。
- 不要让不同节点的范围重叠，不要覆盖 SSH 以外的其他 UDP 服务（DNS、游戏、其他 QUIC 等）。wildcard 匹配本机所有地址，包含本机程序访问这些端口。
- OUTPUT 规则只匹配本机地址，不劫持发往远程服务器的相同 UDP 端口。
- 范围包含 `server_port` 时自动排除该端口的自重定向，正常直接进入监听。
- 容器建议在**测试环境**使用 host 网络及最小 NET_ADMIN 权限。普通 bridge NAT/容器端口映射可能位于不同网络命名空间，不会因容器内规则自动覆盖宿主机；必须单独规划，不默认要求 `--privileged`。
- 本仓库 Dockerfile 已包含 nftables 工具，但这不等于容器自动拥有 NET_ADMIN 权限；容器镜像未在本地完成运行验收。
- 如果公网范围由外部 NAT/负载均衡器处理，不要同时让该范围指向错误的本机地址；本实现用于范围和监听在同一节点网络命名空间的场景。

## 4. 生命周期、重载与故障处理

1. 核心成功创建监听后再创建 NAT 规则；规则安装失败会回滚并关闭监听。
2. nft 使用一次原子批处理；iptables 失败时按反向顺序撤回已创建规则。
3. 删除节点、正常退出或配置重载时清理当前节点规则；清理失败会返回包含表/链名的错误，保留重试能力。
4. 不接管/清空已经存在的同名规则，避免误伤另一进程。不要同时运行相同节点的两个实例。
5. `kill -9`、崩溃、主机重启或防火墙 reload 不保证正常清理/保留规则。本轮不提供规则周期性自愈：防火墙重载后检查规则并正常重启节点。
6. 遗留规则须先停止所属服务、确认无其他进程使用后，按日志中的**精确名字**处理。nft 删除对应 `ip`/`ip6` 表；iptables 先删除 PREROUTING/OUTPUT 对应跳转，再清空/删除专用链。不要用 `nft flush ruleset` 或 `iptables -F` 粗暴处理。

回滚面板补丁前先将节点恢复单端口并让 V2bX 正常重载/退出，确认规则已清理。确认补丁未被其他改动覆盖后，可 `git apply --reverse --check "$PATCH"`，再反向应用。

## 5. 验证

普通 Go 测试不会运行系统防火墙命令；命令生成/失败回滚使用 fake runner。PHP 测试使用真实内存 Eloquent 模型和 `buildNodeConfig`，不连接数据库/Redis，也不启动面板。

Linux 真实端口转发与 QUIC 跳跃测试单独显式启用，必须在一次性 network namespace 中运行。测试同时检查 `/proc/self/ns/net` 不等于 `/proc/1/ns/net`，拒绝在宿主网络空间执行。运行方式见 `scripts/test-porthop-netns.sh`；不要在生产节点执行测试脚本。

仍需用户上线前验收：真实域名/证书、Xboard 用户分配与流量入账、云安全组、客户端订阅更新、多节点端口规划、长期连接和负载。本地测试不等于生产环境已经开通。
