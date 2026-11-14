# P0/P1：节点基础修复与现有传输配置补全

本批次保留 V2bX 的面板节点后端结构，不改变已有协议的面板类型，不增加演示账号，不自动升级依赖版本。

## 已实现

- 核心按配置顺序稳定选择；显式 CoreName 与 Core 冲突、未知核心、不支持的传输组合会报错。
- 配置在候选对象中解析和校验；监听目录以支持原子替换；应用失败尝试恢复上一套配置与运行实例。
- 节点启动失败会回收已启动节点；任务退出等待正在执行的回调，避免旧任务干扰新任务。
- 流量使用原子取数，TCP 多次读写累计计数；待上报数据先保存到本地，失败后重试并支持重启恢复。
- ReportMinTraffic 在待上报数据聚合后应用；未达到阈值的数据保留，不因一次取数而丢弃。
- 用户 ID、设备数和速率变化会触发更新；304 保留最近成功拉取的授权列表。
- 动态限速任务实际启动，采集当前周期数据，Xray/sing 的已建立连接可重新读取限速状态；sing UDP 增加速率包装。
- Xray 的 VMess/VLESS/Trojan 共用 TCP、WS、gRPC、HTTPUpgrade、XHTTP、KCP 参数转换；sing Trojan 增加 HTTPUpgrade。
- Xray/sing 支持 CertConfig 的 ALPN、TLS 版本、密码套件及 ECH 私钥文件。
- REALITY 支持多 ShortIds；Xray 支持多 ServerNames；单值配置继续兼容。
- VLESS encryption 与 Fallback 的未适配组合明确拒绝，不再静默关闭加密。
- 面板请求错误中的原始及 URL 编码 API Key 会脱敏。

## 新增配置项

这些字段在对应节点现有配置中按需添加，不应将下面内容当成可直接连接的完整配置。

| 位置 | 字段 | 含义 |
|---|---|---|
| 节点选项 | TrafficStateDir | 待上报流量保存目录；默认是配置文件同目录下的 traffic；相对路径相对于配置文件 |
| CertConfig | ALPN | 字符串数组；未设置时沿用内核默认值 |
| CertConfig | MinVersion、MaxVersion | TLS 版本字符串，如 1.2、1.3；校验上下界 |
| CertConfig | CipherSuites | Go TLS 密码套件名称数组；TLS 1.3 套件由标准库决定，不能通过这里指定 |
| CertConfig | ECHKeyFile | 包含单个 ECH KEYS PEM 块的文件；当前适配 X25519，要求 TLS 1.3 可用；相对路径相对于配置文件 |
| 面板 tls_settings | server_names | REALITY 服务名数组，Xray 支持多值；sing 需要单个服务名 |
| 面板 tls_settings | short_ids | REALITY ShortId 数组；各元素为最多 16 个十六进制字符且长度为偶数 |

旧 server_name/short_id 与数组同时出现时，单值必须包含在数组中。冲突、重复及非法 ShortId 会被拒绝。

ECH 文件只保存到节点侧，不能写入普通客户端订阅。客户端需要单独取得公开的 ECHConfigList；本批次不替线上面板生成订阅模板或发布 DNS HTTPS 记录。

## 动态限速

启用 EnableDynamicSpeedLimit 后，DynamicSpeedLimitConfig 必须具有正值的 Periodic、Traffic、SpeedLimit、ExpireTime。

- Periodic：统计窗口，单位秒。
- Traffic：窗口内上下行合计阈值，单位字节。
- SpeedLimit：触发后的速率，单位 Mbps。
- ExpireTime：限速有效期，单位分钟。

Xray/sing 的流连接已做实际验证。独立 hysteria2 内核尚未适配这条动态速率链路，启用时明确报错；不要把写入一个限速字段当成已生效。

## 计量及恢复边界

流量文件只包含版本、UID 和待上报字节数，不保存面板密钥。目录及文件需要保持可写，并应随配置迁移。已经确认的数据从待上报列表移除；小于 ReportMinTraffic 的数据继续累积。

现有 UniProxy 增量上报接口没有本批次新增的服务端幂等契约。若面板已入账但响应丢失，节点重试可能重复入账；若确认后的本地记录写入失败，重启也可能再次发送。这一问题不能仅靠节点端做到恰好一次，后续需要面板 BatchID 去重配合。

内存取数与文件持久化之间、关闭连接与最终取数之间仍存在异常退出/并发边界；本批次不承诺断电零丢失。损坏的持久化文件会阻止该节点启动，避免静默丢弃数据。

## 已确认的兼容性限制

- 当前固定 Xray 版本不再应用旧 KCP seed、伪装 header、congestion、readBufferSize、writeBufferSize 参数。本适配拒绝有实际内容的这些旧参数，防止配置看似生效。
- KCP 可用参数以当前内核为准：mtu、tti、uplinkCapacity、downlinkCapacity、cwndMultiplier、maxSendingWindow；不支持 TLS/REALITY 组合。
- Xray 26.11.9 的默认 HTTPUpgrade 客户端会发送 Sec-WebSocket-Key，内嵌 sing-box 会拒绝该请求。已分别验证 Xray 客户端到 Xray 服务端、sing 原生客户端到 sing 服务端；不宣称该跨内核默认组合互通。
- sing 不支持本适配的多 REALITY ServerNames 和 RejectUnknownSni；需要这些选项时选择 Xray。
- 独立 hysteria2 的高级 TLS 参数继续通过 Hysteria2ConfigPath 配置，不接受本批次的公共 CertConfig 高级参数。
- 同端口重载可能短暂断开当前节点连接；恢复旧实例需要资源可重新取得，不承诺所有错误下都能无损切换。

## 验证入口

沿用仓库完整 BUILD_TAGS，运行 Go 测试。新增 transport_dataplane_test.go 使用真实内核及回环客户端验证 KCP、XHTTP、Trojan HTTPUpgrade、TLS/ECH、REALITY 和已建立连接上的动态限速。

Linux 成品运行验收：

```bash
python3 scripts/test-runtime.py /absolute/path/to/V2bX
```

该脚本只创建临时目录、回环面板和回环代理，验证正常转发、503 重试、文件持久化、错误配置、运行实例回滚、原子配置更新、用户撤销/恢复、SIGTERM 退出及启动失败退出码，不访问生产面板、公共 CA，也不修改系统服务和防火墙。

## 后续批次

本批次没有接入 XDRIVE、MASQUE、WireGuard、HTTP/Mixed、Tunnel/TUN、AmneziaWG、MTProto 的新面板节点类型。相关内核可用性不等于管理链路完成，后续按适配方案继续实施。
