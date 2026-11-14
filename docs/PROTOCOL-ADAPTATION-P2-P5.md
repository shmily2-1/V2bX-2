# P2–P5 协议与本地服务适配

本次扩展保留现有面板节点，通过可选的本地服务配置管理 Tunnel、TUN、WireGuard、AmneziaWG 和 MTProto。本说明描述节点端能力；Xboard 数据库、后台表单、服务同步和订阅适配在独立面板项目交付，需要另外部署。

## 能力与边界

| 范围 | 实现方式 | 已覆盖的运行验证 |
|---|---|---|
| HTTP / Mixed | sing 入站；`uuid` 作为用户名，新增显式 `password`；认证快照与连接撤销 | CONNECT、SOCKS TCP/UDP、TLS、空授权/错误密码拒绝、用户撤销、UID 计量、导出客户端实际连接 |
| Tunnel | 独立 sing 实例，TCP/UDP 转发 | 真实成品启动、CLI 管理、TCP/UDP、总流量、端口释放、失败配置恢复、进程重启 |
| TUN | 独立 sing 实例，网卡/地址/路由/出口管理 | Linux 隔离环境中的网卡生命周期、TCP/UDP 转发、自动路由及策略规则回收 |
| XDRIVE | 已锁定 Xray 的 VLESS 传输 | local 存储、VLESS Encryption、Mux、真实文件通道、UID 计量、撤销、客户端配置导出 |
| MASQUE | 已锁定 Xray 的 CONNECT-IP 代理与传输 | H2、H3、TLS、空授权拒绝、TCP 转发、UID 计量、撤销、导出客户端连接 |
| WireGuard | 每服务独立的 sing 用户态 Endpoint | 双 Peer 真实加密转发、动态增删、UID 计量、稳定分配、地址回收、密钥匹配导出、服务重启 |
| AmneziaWG | 固定版本独立进程，UAPI 控制 | 官方源码构建的后端、真实跨命名空间加密转发、握手、Peer 计量/撤销、配置导出、网卡回收 |
| MTProto | mtg 2.1.7；每个 Secret 一个独立实例 | 官方发行包、真实 FakeTLS HMAC 认证、Prometheus、进程/监听回收、Telegram 链接导出 |

Xray 继续使用 `v1.260327.1-0.20261109052709-676eaf66f8f0`。`go.mod`、`go.sum` 保持不变。新增的内嵌 sing 兼容接口已纳入补丁清单。

以下边界保持明确：

- 未取得实际远端存储账号，因此 XDRIVE 的远程云存储互通未验收。local 只证明共享文件通道工作，不能用来证明两台机器自动共享本地目录。
- MASQUE 的外层设备来源没有进入固定 Xray 的用户分流接口，`DeviceLimit` 会被拒绝，避免将隧道地址数冒充真实设备数。连接数限制不是完整的隧道会话配额。IPv6 数据转发、ICMP 和长期并发没有作为本轮已通过项。
- WireGuard/AmneziaWG 的 Peer UID 是本地服务归属，不通过 UniProxy 节点用户接口管理。配套 Xboard 使用独立服务同步、应用确认和流量批次接口，原版面板不会自动获得这些能力。
- MTProto 当前提供每实例总量。真实 FakeTLS 认证已验证，Telegram DC 的公网连接、收发消息和生产环境吞吐未验证。
- 系统网卡和外部后端在 Linux 隔离环境验收；其它系统的 TUN 驱动与外部后端发行包没有据此宣称可用。AmneziaWG 适配器明确要求 Linux。

## HTTP / Mixed / MASQUE 面板契约

`NodeType` 新增 `http`、`mixed`、`masque`。HTTP/Mixed 选择 `sing`，MASQUE 选择 `xray`。现有配置向导和部署协议列表同步扩展。

用户接口仍为 `/api/v1/server/UniProxy/user`，每个新协议用户需要：

| 字段 | 含义 |
|---|---|
| `id` | 正整数 UID |
| `uuid` | 线上的认证用户名，与内部节点计量标识分开 |
| `password` | 显式密码；不会把缺失密码静默当作匿名访问 |
| `speed_limit` | 现有用户速率配置 |
| `device_limit` | HTTP/Mixed 使用原有路径；MASQUE 暂拒绝非零值 |

HTTP/Mixed 用户名不能包含冒号、竖线或控制字符，用户名与密码最长各 255 字节。删除或改密会更新认证快照并撤销已路由连接，旧认证快照不能在重新添加同名用户后复用。

HTTP/Mixed `/config` 使用 `server_port`、`host`、`server_name` 和 `tls`（0/1）。TLS 模式必须启用证书。

MASQUE `/config` 除通用字段外使用 `address`（IPv4/IPv6 地址池列表）、`mtu`、`path`、`masque_host`。证书在节点 `CertConfig` 中管理，`ALPN` 可指定 `h2` 或 `h3`；同时开放两种传输时服务器可能同时使用 TCP 和 UDP。空 ALPN 按当前 Xray 默认选择 H3。

## XDRIVE

使用 `NodeType: vless`、`Core: xray`、`network: xdrive`，存储配置放在 `network_settings`。

固定 Xray 要求：

- VLESS Encryption 必须启用，`flow` 必须为空。
- 服务端 `encryption` 为 `mlkem768x25519plus`，`encryption_settings` 继续使用 `mode`、`ticket`、`server_padding`、`private_key`。TLS/REALITY 不能叠加到本适配的 XDRIVE。
- 客户端必须启用 Mux；导出器自动配置 Mux 并从服务端私钥派生客户端所需的公钥，客户端导出不包含服务端私钥。

可传入锁定内核的 `service`、`remoteFolder`、`secrets`、`segmentBytes`、`flushIntervalMs`、`pollIntervalMs`、`maxPollIntervalMs`、`sessionTtlSeconds`、`concurrency`、`eagerWindowMs`、`holeTimeoutMs`、`template`、`s3`、`xmux`。内核支持的后端包括 local、s3、google-drive 和 template；只有 local 已完成本轮真实数据面验证。

XDRIVE 不以 TCP 监听端口作为健康证据。现有在线部署器会明确拒绝将其套用普通端口健康检查；通过手工配置、实际存储会话和导出客户端验证。

```sh
V2bX export-node -c /etc/V2bX/config.json --node-index 0 --uid USER_UID --server NODE_HOST -o /private/client.json
```

导出远端 XDRIVE 必须显式加 `--allow-storage-credentials`。此开关不会自动隔离云盘权限；操作方应先提供专属目录和受限凭据。导出文件不能放在公开订阅目录。

## 本地服务管理

`LocalServiceDir` 默认是主配置所在目录下的 `services`，也可显式指定。主配置可保留现有 `Cores` / `Nodes`；纯服务部署允许它们为空数组。

主配置中的 `LocalServices` 可用于声明服务。需要 CLI 增删改和启停的服务，保存到 `LocalServiceDir` 下，每个服务一个 `ID.json`。两处不能重复声明相同 ID。CLI 管理单个服务时无需重载其它面板节点或服务。

```sh
V2bX services -c /etc/V2bX/config.json list
V2bX services -c /etc/V2bX/config.json list --type wireguard
V2bX services -c /etc/V2bX/config.json put -f /private/service.json
V2bX services -c /etc/V2bX/config.json disable --id SERVICE_ID
V2bX services -c /etc/V2bX/config.json enable --id SERVICE_ID
V2bX services -c /etc/V2bX/config.json restart --id SERVICE_ID
V2bX services -c /etc/V2bX/config.json remove --id SERVICE_ID
```

`v2bx services ...` 和 `v2bx export-node ...` 也会转发给当前安装的程序。

管理写入使用文件锁、私有临时文件和原子替换，修改/移除前保留带时间戳的原配置。后台每秒读取配置并应用；写入成功表示配置已保存，实际执行结果通过 `list` 查看。失败候选会保留或恢复旧实例，保持失败状态；相同失败候选不反复重启当前服务，修正配置或执行 `restart` 后重试。

状态文件包含真实状态、错误、服务/UID 流量、适用的 Peer 地址/握手/外层 Endpoint，以及外部进程 PID/退出码。超过 5 秒没有更新的状态显示为过期。状态“运行中”表示实例及适用的健康检查正常，不表示已经完成公网端到端连接验收。

### 通用字段

| 字段 | 内容 |
|---|---|
| `ID` | 稳定 ID，字母/数字/下划线/连字符，最长 48 字符 |
| `Type` | `tunnel` / `tun` / `wireguard` / `amneziawg` / `mtproto` |
| `Enabled` | 启用开关，缺省为 false |
| `Listen` | IP:端口；TUN 不使用此字段 |
| `Revision` | CLI restart 更新的重启代次 |

### Tunnel / TUN

Tunnel 使用 `Target`（主机:端口）与 `Network`（`tcp`、`udp` 或 `tcp,udp`），不创建伪造面板用户，流量记为服务总量。

TUN 的 `TUN` 对象字段：

| 字段 | 内容 |
|---|---|
| `Interface` | 本服务创建的网卡名 |
| `Address` | 网卡地址前缀列表 |
| `MTU` | 可选 MTU |
| `AutoRoute` | 显式自动路由开关，默认 false |
| `RouteAddress` / `RouteExcludeAddress` | 接管/排除的目标网段 |
| `OutboundInterface` | 可选出口网卡；设置时启动前检查存在性 |

TUN 使用原始目标转发，默认禁用 DNS 劫持；自动选择出口接口以避免再次进入 TUN。只在启用服务时创建资源，关闭时由独立实例释放。没有自动配置宿主 NAT 或默认开通公网转发。

### WireGuard / AmneziaWG Peer

`WireGuard` 对象：

| 字段 | 内容 |
|---|---|
| `PrivateKeyFile` | 本机服务端 Base64 私钥文件；相对路径按配置目录解析 |
| `Address` | 每个地址族最多一个地址池；地址部分作为服务端保留地址 |
| `MTU` | 默认用户态 WireGuard 为 1408，AmneziaWG 为 1420 |
| `Endpoint` | 客户端导出所使用的公网主机:端口 |
| `DNS` | 客户端 DNS 地址列表 |
| `ClientAllowedIPs` | 客户端允许路由；缺省按已配置地址族生成默认路由 |
| `Peers` | Peer 数组 |

每个 Peer 使用 `UID`、`PublicKey`、可选 `PreSharedKey`、`Address`、`KeepAlive` 和 `SpeedLimit`。`Address` 只能为池内唯一的 `/32` 或 `/128` 主机前缀，避免用户任意声明其它 Peer 的源地址。缺失地址由持久化分配器补齐；UID 重排或换公钥保留地址，删除后可回收。不同服务地址池不能重叠。

```sh
V2bX services -c /etc/V2bX/config.json peers list --id SERVICE_ID
V2bX services -c /etc/V2bX/config.json peers put --id SERVICE_ID -f /private/peer.json
V2bX services -c /etc/V2bX/config.json peers remove --id SERVICE_ID --uid USER_UID
V2bX services -c /etc/V2bX/config.json export --id SERVICE_ID --uid USER_UID --client-key-file /private/client.key -o /private/client.conf
```

客户端私钥由客户端持有；导出时检查其公钥匹配 Peer。已有导出文件不会被静默覆盖。列表会隐藏 PSK。

WireGuard 使用每服务独立的用户态设备，不依赖系统 WireGuard 模块，也不新增宿主默认路由。其 UDP socket 使用通配地址，所以 `Listen` 应为 `0.0.0.0:端口` 或 `[::]:端口`。Peer 更新不重启其它服务；会撤销改换身份/地址的连接。计量使用认证 Peer 的原生收发字节，包含 WireGuard 协议开销，处理计数重置与删除后重加；TCP/UDP 使用单独的可限速路径。其它 IP 协议的实际转发和速率限制未作为本轮已验证能力。

### 独立后端

`Backend` 的公共字段为 `Executable`（绝对路径）、`SHA256` 和 `Version`。启动前检查程序哈希，监听/UAPI/指标与进程状态均参与实际检查。外部后端的原始输出可能包含 Secret，因此不直接写入通用日志。

AmneziaWG：

- `Version`: `amneziawg-go-v3.1.20260828`。
- `Interface`: 独立网卡名；`ControlSocket`: `/var/run/amneziawg/网卡名.sock`。不会接管已有同名网卡。
- `Obfuscation`: 支持 `jc/jmin/jmax/s1/s2/s3/s4/h1/h2/h3/h4/i1/i2/i3/i4/i5`。数值和范围进行输入校验；本轮实际互通覆盖基础 J/S/H 参数，未覆盖所有高级组合。
- 复用 Peer、地址分配、PSK 和导出；按后端 Peer 的收发字节计量，含加密开销。`SpeedLimit` 暂不支持非零值。
- 本服务配置网卡地址与 MTU，不自动启用宿主 IP forwarding/NAT；作为公网网关使用时需已有相应系统网络配置。

MTProto：

- `Version`: `mtg-2.1.7`。
- `SecretFile`: `ee` 开头的十六进制 FakeTLS Secret 文件。
- `MetricsListen`: 非公开的回环 IP:端口，必填。
- `Concurrency`: 正整数；`PublicIP`: 客户端链接使用的 IP。
- 每个实例只管理一个 Secret。修改 Secret 文件后执行该服务的 `restart`，会停止该实例的原连接。按实例统计 Telegram 方向流量，不伪造用户级账单。
- 使用 `services export --id SERVICE_ID -o /private/telegram-link.txt` 保存客户端链接，不在终端输出 Secret。

## 状态、备份与恢复

| 路径 | 用途 |
|---|---|
| `LocalServiceDir/ID.json` | CLI 管理的服务配置 |
| `LocalServiceDir/ID.json.bak-*` | 修改/删除前的原配置 |
| `LocalServiceDir/.state/status.json` | 状态和累计流量 |
| `LocalServiceDir/.state/ID.addresses.json` | UID 地址分配 |
| `LocalServiceDir/.state/ID.mtg.toml` | 本程序生成的私有 mtg 运行配置 |
| `LocalServiceDir/.state/runtime.lock` | 防止多个运行实例同时拥有同一状态目录 |

停止服务后再迁移配置、状态目录、私钥文件及对应版本后端。文件配置回滚不等于回滚已被外部修改的私钥/Secret 文件；这些文件需单独备份。

计量每秒汇总并原子落盘，正常关闭会采集计数。突然终止/断电可能丢失最后一次汇总后或外部进程停止间隙的在途流量；这不是恰好一次的结算账本。面板原有待上报机制仍按 P0/P1 的边界工作。

## 验证方式

完整构建标签：

```text
sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor
```

新增核心数据面测试为 `TestExtended*`、`TestExported*`、`TestLocal*`。外部后端测试为 `TestRealAmneziaWGBackend` 与 `TestRealMTProtoBackend`，必须在预置后端且隔离网络/挂载命名空间中显式运行。TUN 系统测试也有同样的隔离要求。

`scripts/test-local-services.py` 使用真实成品和 CLI 验证配置、状态、TCP/UDP、备份、失败恢复、启停和重启持久化；测试目录通过 `--workdir` 指定，与正式配置隔离。所有具体命令结果和未验证项见本轮交付报告。

`scripts/test-protocol-services-netns.py` 创建一次性的网络和挂载命名空间，可同时运行核心测试二进制以及显式提供的 AmneziaWG/mtg 后端测试。它不下载或安装后端，也不修改宿主路由；后台程序和编译后的测试二进制由调用方通过参数指定。CI 已加入本地服务真实成品验收与隔离核心协议测试，独立后端测试仍需显式预置对应版本。
