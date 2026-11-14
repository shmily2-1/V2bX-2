# REALITY 握手兼容性排查与修复

## 核实范围（2026-11-14）

本次读取了用户提供视频的标题、简介、章节及其引用的上游 issue，未取得视频逐字字幕，不能把简介中的测试结果当成本项目的实测结果。

视频：https://www.youtube.com/watch?v=f-3VNUOmH74

上游证据：

- https://github.com/SagerNet/sing-box/issues/4520
- https://github.com/XTLS/Xray-core/issues/7259
- https://github.com/XTLS/REALITY/commit/8cdf7bf9c7f09cb9814bf08c3eb877f68b85fba8
- https://github.com/iskhakovt/sing-box/commit/dfd19d104fd1dbed9428694e166bf79283c73686

前两项是兼容性报告；服务端的实际 ClientHello 检查与本项目内嵌客户端的过滤逻辑才是本次代码修复的直接依据。不将视频对维护者的评价作为技术结论。

## 根因与修复

项目当前内嵌 sing-box v1.15.0-alpha.13，uTLS 为 metacubex/utls v1.8.7。`common/tls/reality_client.go` 在构造 ClientHello 后，无条件从 supported_groups 和 key_share 删除 `X25519MLKEM768`。

项目所用新版 Xray REALITY 服务端要求在独立 X25519 share 之前出现有效的 X25519MLKEM768 share。缺失时，连接可能被转交伪装目标，客户端最终出现 `reality verification failed` 或收到目标站的真实证书。相同报错也可能有其他原因，不能单凭字符串确定是此缺陷。

修复规则：

| 指纹携带的内容 | 处理 |
|---|---|
| ML-KEM group 和 share 均存在 | 保留原有顺序及密钥数据 |
| 只有 group 或只有 share | 同时移除两侧的 ML-KEM 项，避免 HelloRetryRequest / illegal_parameter |
| 两者均不存在 | 保持指纹原状，不伪造密钥或替换指纹 |

REALITY Session ID 的认证继续使用原有独立 X25519 ECDHE 密钥。没有修改服务端认证、UUID、证书验证、版本限制或 REALITY 客户端版本字节。

## 本项目能修复什么

- 修复 V2bX 内嵌 sing-box 作为客户端的连接，包括 `OriginalPath` 中配置的 REALITY 上游出站。
- 对支持原生完整混合 share 的指纹（如当前依赖中的 `chrome`）保留其 ML-KEM 数据。
- 保留不完整 randomized 指纹的配对清理，避免简单删除整段过滤带来的回归。

## 本项目不能替外部客户端修复什么

V2bX 服务端更新不会改变手机、路由器或桌面客户端已经发送的 ClientHello。未打补丁的 Karing、Hiddify 或独立 sing-box 仍可能失败；需要更新对应客户端内核，或改用已验证的 Xray 客户端。

不会自动把 Xray 服务端换为 sing，不会降级 Xray，也不会移除其 ClientHello 检查。没有混合 share 的 `firefox/safari/edge` 等指纹不能因为此补丁就宣称兼容。

面板明确设置的 `minClientVer` 是独立条件，不能靠 ML-KEM 修复消除。保留管理员设定，避免通过伪造版本字节掩盖问题。该修复也不提供 sing-box 的 XHTTP 支持。

## VLESS 与 HY2 分开排查

| 失败阶段 | 检查事项 |
|---|---|
| TCP 连接未建立 | 监听端口、路由、安全组、转发 |
| REALITY 认证失败 | SNI、公钥/私钥、short ID、时钟、版本限制、ClientHello 的 group/share |
| REALITY 成功但 VLESS 被拒绝 | 面板是否分配用户、UUID、flow、传输参数 |
| HY2 超时 | UDP 是否到达实际监听端口、端口跳跃 NAT、混淆参数 |
| HY2 TLS 失败 | 证书域名、有效期、信任链及客户端时间 |
| HY2 认证失败 | 用户是否已从面板加载、认证凭据 |

HY2 使用 QUIC/TLS 和独立代理认证，不使用本次修复的 REALITY ClientHello。不能为修复 REALITY 而取消 HY2 证书验证，也不能把“服务已启动”当作客户端握手成功。

Xray v26.11.9 启动时会异步探测伪装目标的握手后记录；探测完成前，已认证连接可能按五秒间隔等待。新增测试等待真实探测状态就绪，不预填或绕过探测缓存，避免将冷启动等待误判为认证失败。

## 回归验证

新增：

- `third_party/sing-box/common/tls/reality_keyshare_test.go`：完整、不完整、缺失 group/share；Chrome 原生密钥；实际握手路径写出的 ClientHello。
- `core/reality_handshake_test.go`：内嵌 sing REALITY 客户端连接 V2bX 的 Xray/sing 服务端，分别使用仅 X25519 和支持 ML-KEM 的本地 TLS 1.3 伪装目标；验证合法 UUID 的 VLESS echo 和非法 UUID 的拒绝。
- CI 显式执行内嵌模块的 REALITY 测试，因为根目录 `go test ./...` 不会自动遍历嵌套 Go module。

所有新增监听均在 loopback，使用临时证书和测试用户，不访问公网伪装站、面板或生产服务器。现有 `TestUpgradedCoreDataPlane` 继续检查 VLESS、sing HY2、独立 HY2 的认证、双向转发、流量计数和用户撤销。

测试命令：

```bash
export GOEXPERIMENT=jsonv2 CGO_ENABLED=0
TAGS='sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor'
go test -tags "$TAGS" -count=1 -timeout 60s -run '^TestReality' github.com/sagernet/sing-box/common/tls
go test -tags "$TAGS" -count=1 -timeout 120s -run '^TestRealityHandshakeInteroperability$|^TestUpgradedCoreDataPlane$' ./core
python3 scripts/check-sing-patches.py
```

最终验证状态以实际执行结果为准；新增测试源码不等于已通过测试，不等于外部客户端或公网节点已完成验收。

2026-11-14：提交 `10ee9b4` 对应 CI run `43584282417` 的全包测试、内嵌 REALITY 测试及补丁可复现检查均通过。覆盖四种回环服务端/目标组合、合法用户数据回传和非法 UUID 拒绝；本机离线编译曾因缓存缺少锁定依赖而未能执行，验证由 CI 完成。未声称进行公网、生产面板或外部手机客户端验收。
