# 本次验收记录

记录时间：2026-09-20（Asia/Shanghai）。环境：Windows + 本机 WSL2 Debian；没有连接或改动用户生产节点、面板数据库、证书或宿主网络防火墙。

## 通过的检查

| 检查 | 结果 / 范围 |
|---|---|
| `go mod verify` | 模块校验通过 |
| `scripts/check-sing-patches.py` | 官方 sing-box v1.14.1 + 13 个兼容源码变更校验通过 |
| 全内核 Go 测试 | `sing xray hysteria2 with_quic with_grpc with_utls with_wireguard with_acme with_gvisor`，全包通过 |
| 三内核生命周期 | 13 个核心/协议组合，创建/删除节点、用户增删及重新加入 |
| 原有真实回环数据面 | Xray/VLESS、sing/VLESS、sing/Hysteria2、独立 Hysteria2；TCP echo、认证、流量、删除用户后的新连接拒绝 |
| Xboard 解析 | Hysteria1、显式 Hysteria2、`hysteria/version=2`、旧单端口响应、空字段、错误范围/间隔、配置缓存、传输失败 |
| 端口规则单元测试 | 无系统副作用；解析、IPv4/IPv6、nft 原子命令、iptables 回滚/逆序清理、失败重试、非 Linux 拒绝；包语句覆盖率 93.1%（Windows） |
| 真实 UDP NAT | 隔离 network namespace；nftables、iptables-nft、iptables-legacy；具体 IPv4/IPv6、IPv4 wildcard、双栈 wildcard；范围端点、监听端口排除、重复规则拒绝、幂等清理 |
| 真实 QUIC 跳跃 | 三种防火墙后端分别测试独立 Hysteria2 和 sing；范围 `20000-30000` → `18443`，IPv4/IPv6 固定范围端点连接、客户端每 5 秒跳跃时同一 TCP 流保持 echo、UID 双向统计、删除/重载/关闭后规则清理 |
| 失败回收 | 两套内核配置错误后 UDP 端口可重新绑定；同 tag 重建；关闭后拒绝再新增监听 |
| Xboard 补丁 | 基于 `4f48e61a2cbc6db5338872b6bdb45ef954ec1256` 的干净源码 `git apply --check` + 应用成功；PHP 8.2.20 语法检查；PHPUnit 11.5.27：6 tests、19 assertions 通过 |
| 构建矩阵 | Linux amd64、Linux arm64、Windows amd64、macOS arm64 四种产物构建通过 |
| Git 检查 | 第一方新增内容常见密钥格式扫描无命中；`git diff --cached --check` 通过；未更改第三方原许可证格式 |

PHP 测试使用与 Xboard lock 一致的 Laravel 12.54.1 和 PHPUnit 11.5.27，独立安装到测试目录，禁用 Composer 插件/脚本；不启动 Laravel 应用、不运行迁移。该最小依赖环境不是部署用面板，Composer audit 的旧依赖提示也不代表本次完成了 Xboard 全依赖安全审计。

## 不应误读为已完成的内容

- Linux namespace 测试使用本机 loopback，验证实际内核、UDP NAT 和 QUIC 迁移；不证明公网路径、云安全组、外部 NAT、真实订阅客户端已经配置正确。
- 未对生产 Xboard 应用补丁，未验证生产用户分配、面板计费入账或公网 TLS/REALITY。
- macOS/ARM 构建是交叉编译，不是这些设备上的运行验收。端口跳跃只支持 Linux。
- Dockerfile/CI 工作流已更新，但不能把本地结果当成 GitHub Actions 或 Docker 运行已经成功。
- 未完成长期并发、性能/限速精度和所有协议/客户端组合的验收；正常退出清理不覆盖 `kill -9` 和防火墙外部重载。
- 本轮仅发布源码和本地验收包，不自动部署节点，也不自动发布 stable Release。
