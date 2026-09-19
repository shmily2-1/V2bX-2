# 内核升级与验收说明

## 1. 本次变更

- 基于 wyx2685/V2bX `dev_new`，保留原历史，模块改为 `github.com/shmily2-1/V2bX-2`。
- Xray 改为官方 `v1.260327.0`，保留本项目 dispatcher 的用户计数、审计和限速；去掉官方已经删除的传输伪装头导入，补上 HTTPUpgrade 注册。
- 修复空 NetworkSettings 时 Xray StreamSetting 为空导致的 panic，TCP 默认配置无需再传空对象占位。
- Hysteria core/extras 同步更新到 `v2.12.3`，迁移 Salamander API，并在混淆配置失败时释放 UDP socket。
- sing-box 更新为官方 `v1.14.1` 源码快照，在 `third_party/sing-box` 保留可审核的动态用户补丁。模块 checksum/官方 commit/每个补丁文件哈希均记录在 manifest，CI 检查漂移。
- sing-box 新增证书 provider registry 和 RoutedFlow 接口适配；移除 fork 的 GetCtx，使用已注入 service registry 的官方 context。
- gVisor 对齐 sing-box 1.14.1 所需的 `v0.0.0-20260727.0-sing-box-mod.1`，修复旧版本残留导致 macOS `header.IsExtensionHeader` 缺失的构建错误。
- 用户删除关闭连接时采用 `io.Closer`，避免把 UDP PacketConn 强转 net.Conn 的 panic；VLESS/Trojan/AnyTLS 的连接跟踪放在认证后，确保记录真实用户。
- 更新命令不再运行旧上游脚本；卸载时用户未输入 y 立即返回，修复“取消后继续卸载”的问题。

## 2. 必须注意的兼容变化

新增：Hysteria2 端口跳跃同时覆盖独立和 sing 内核。仅明确下发 `ports` 才创建 Linux NAT 规则；正常删除/重载/退出清理。Xboard 配套补丁、固定监听端口与公网范围的区别、权限和故障处理见 `HYSTERIA2-PORT-HOPPING.md`。该功能未修改任何线上服务器。

1. **Reality Xver**：sing-box 官方没有旧 fork 的该字段；非零值明确报错，需改用 `xray`，不静默丢弃。
2. **Xray IVCheck 与旧 mKCP 伪装头**：官方已经移除相应接口；`DisableIVCheck` 不再控制新版内核。不要假定旧传输配置仍完全兼容。
3. **外部 sing-box 原生配置**：`OriginalPath` 中 DNS、路由、TLS 等字段需符合 1.14.1。解析不通过时应人工迁移，不能盲目覆盖旧配置。
4. **Hysteria2 独立内核与 sing 实现不同**：二者各自升级，不能把 sing 的版本号当作官方 Hysteria core 的版本。新版其他可选功能不保证已暴露到面板字段；独立内核此轮仍支持 plain/Salamander 混淆。
5. **TUN**：RoutedFlow 是新增接口占位；V2bX 面板管理的是代理 inbound，不为外部原生 TUN fast-path 流量提供面板用户计费。
6. **在线变更限制**：保留上游动态用户管理方式，不承诺所有既有多路复用/QUIC 会话均能立即撤销，也未完成高并发用户热更新 race/长期稳定性验收。
7. **架构范围**：本轮发布矩阵为 Linux amd64/arm64、Windows amd64、macOS arm64；上游其它小众架构不列入已经验证的发行范围。

## 3. 安全升级步骤

1. 保存 `systemctl cat V2bX` 输出及旧二进制 SHA256；备份 `/etc/V2bX`（含证书）和实际 ExecStart 指向的程序。
2. 在备用节点或独立端口启动新版本。不要两个程序绑定同一端口，不修改现网 ApiHost、NodeID、token。
3. 验证客户端实际连接、TLS/REALITY、TCP/UDP、多节点用户隔离、订阅协议参数；通过面板核对用户增删、流量方向、计费、在线 IP、限速与审计。
4. 用户 API 返回空列表属于面板数据/分配问题，不是内核升级能自动修复；不要伪造用户绕过。
5. 验证后由维护者切换服务；观察资源占用和日志。失败立即恢复旧二进制和旧配置。

## 4. 后续如何更新所有内核

不要对整个依赖图无差别运行 `go get -u ./...` 后直接发布。

1. 查询官方 Releases，区分 stable/prerelease；记下准确 tag 与 commit。
2. Xray 更新其 Go module 版本；Hysteria 同步更新 core 和 extras，确认两者版本一致。
3. sing-box **不能只改 go.mod**：下载新的官方源码，对 `third_party/sing-box-patches.json` 列出的补丁逐个重放/迁移，并审查 protocol 库、用户标识和已有连接处理。保持许可证与来源。
4. 更新 `scripts/check-sing-patches.py` 的目标版本，人工审查补丁后运行 `--write` 生成新 manifest；然后正常模式检查。
5. `go mod tidy`、`go mod verify`、全内核测试、各核心构建、Linux 双架构交叉构建；再进行真实客户端与面板验收。
6. 提交源码和 go.sum，再推送 prerelease 标签；仅在验收后由维护者将 release 标记为稳定。

## 5. 已有测试的边界

`core/upgrade_test.go`：三核心启动、13 个核心/协议组合的节点增删和用户增删/重新加入。全部监听 loopback，不申请公网证书。

`core/dataplane_test.go`：使用真实 VLESS 和 Hysteria 2 客户端连到真实本地内核，再转发到本地 TCP echo 服务。断言 payload 一致、UID 对应双向流量、删除用户后新的认证被拒绝。

未将这些测试描述为公网节点、真实面板计费、全协议跨客户端互通或长期稳定性证明。
