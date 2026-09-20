# .8 TCP80占用与证书复用回归记录

发布前检查：安装器26项、bootstrap/菜单36项、在线配置/维护27项、HTTP端口授权/身份/恢复18项，共107项通过；Bash语法、ShellCheck和Python语法通过。WSL原生Linux临时目录执行，真实隔离netns验证占用TCP80的独立进程先TERM、5秒后KILL及pidfd身份固定。

指定服务器另外创建无外网连接的临时netns及测试systemd服务：取消释放保持原PID；确认后真实停止、端口空闲、恢复；注入部署失败后恢复；真实独立进程TERM/KILL。生产配置/证书/私钥/ACME文件哈希及生产节点PID未改变；测试unit和netns均清理。未请求公共CA，不将隔离测试描述为新的证书签发。

修复逻辑保留在既有provision.py，旧.7安装器也会安装该文件，无新增运行时模块缺失问题。发布CI仍将重跑全部测试和四平台构建，发布/上线状态以最终报告为准。

## .7 安装流程回归记录（历史）

发布前本地检查：安装器26项、bootstrap/菜单/配置向导36项、在线配置/维护21项，共83项通过；Bash语法、ShellCheck和Python语法通过。运行于原生Linux的WSL临时目录，未通过递归删除测试触碰宿主真实配置/证书。

新增场景包括：同一菜单完整卸载后选1重装、首次配置逐个节点Y/N交互（大小写均可、默认N）、独立一键入口重装、私密配置/证书/ACME账户/项目备份清除、保留模式、下载/语法失败、挂载/符号链接/自定义服务保护，以及二进制已删除和服务不存在的边界。在线逐节点向导使用真实回环HTTP面板验证节点ID请求；不是多个生产节点上线验收。

发布流水线仍会重新运行Go全量测试、真实隔离网络命名空间、Debian12兼容与四平台构建，所有通过后才创建prerelease。下方为早期.2阶段的历史记录，不应将当时“未部署生产”或旧测试数量当作本次最终状态；部署后的脱敏验收报告另行交付。

## 早期 .2 验收记录（历史）

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
| 原项目兼容安装器 | Bash 语法、ShellCheck、Python 编译检查通过；核心安装器 26 项、bootstrap/菜单/配置向导 19 项离线隔离测试通过 |
| GitHub Actions / Release | 提交 `6f2034c` 的 main 和标签工作流均成功；installer、Go 测试、nftables/iptables 端口跳跃、四平台构建及 prerelease 发布全部成功 |
| 真实 Release 隔离安装 | `v0.1.0-core-upgrade.2` 的 `install.sh` 与 Linux amd64 ZIP SHA256 通过；ZIP 含 management bundle 与全部运行时资源；真实二进制报告 Xray 26.3.27、sing-box 1.14.1、Hysteria2 2.12.3、Go 1.27.1 |
| 更新保护验收 | 在原生 Linux 临时根重复执行固定标签安装，既有 `config.json`、systemd unit、自定义 `route.json` 字节不变；旧二进制生成备份；隔离菜单拒绝宿主服务/日志操作，未发现旧仓库远程安装 URL |
| Git 检查 | 第一方新增内容常见密钥格式扫描无命中；`git diff --cached --check` 通过；未更改第三方原许可证格式 |

PHP 测试使用与 Xboard lock 一致的 Laravel 12.54.1 和 PHPUnit 11.5.27，独立安装到测试目录，禁用 Composer 插件/脚本；不启动 Laravel 应用、不运行迁移。该最小依赖环境不是部署用面板，Composer audit 的旧依赖提示也不代表本次完成了 Xboard 全依赖安全审计。

## 不应误读为已完成的内容

- Linux namespace 测试使用本机 loopback，验证实际内核、UDP NAT 和 QUIC 迁移；不证明公网路径、云安全组、外部 NAT、真实订阅客户端已经配置正确。
- 未对生产 Xboard 应用补丁，未验证生产用户分配、面板计费入账或公网 TLS/REALITY。
- macOS/ARM 构建是交叉编译，不是这些设备上的运行验收。端口跳跃只支持 Linux。
- GitHub Actions 与 prerelease 发布已经成功；Docker 镜像工作流仍未在本轮人工触发，不能据此声称 Docker 镜像已验收。
- 未完成长期并发、性能/限速精度和所有协议/客户端组合的验收；正常退出清理不覆盖 `kill -9` 和防火墙外部重载。
- 已发布 `v0.1.0-core-upgrade.2` prerelease / 验收版，但没有发布 stable Release，也没有自动部署任何节点。
