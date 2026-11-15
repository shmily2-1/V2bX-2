# v0.1.0-core-upgrade.24：Xboard VLESS 完整组合与 XDRIVE 本地回环验证

这是 prerelease / 验收版。相对 .23，本版本同步一键安装脚本和管理菜单版本固定值，保留 Xray 26.11.9、sing-box 1.15.0-alpha.13、Hysteria 2.13.0 锁定内核，并完成 XDRIVE 本地共享存储回环验证。远端 S3/Google Drive 凭据不随安装包分发。

---

# v0.1.0-core-upgrade.23：停用本地服务运行时并移除服务管理组件

- 停用 Tunnel、TUN、WireGuard、AmneziaWG、MTProto 本地服务运行时。
- 保留历史服务配置文件，不再由 `server` 进程加载。
- 一键安装器、管理器和发布文档统一更新到 `.21`。

---

# v0.1.0-core-upgrade.20：接通 Xray Hysteria2 入站适配

本版本将锁定的 Xray Hysteria2 内核能力接入 V2bX，并同步一键安装脚本。安装器默认固定到本预发布版本，确保下载的二进制、管理组件和脚本来自同一个 Release。

- Xray Core 新增 Hysteria2 入站注册、用户认证和动态用户管理。
- 接入 Salamander、Gecko experimental、Gecko 分片范围、UDP idle timeout、QUIC Path MTU 和 Masquerade 配置。
- Xboard 原节点管理页面可选择 V2bX 后端并保存 Hysteria2 配置。
- 一键安装、管理器和安装文档统一更新到 `v0.1.0-core-upgrade.20`。

当前版本仍需在目标环境使用实际证书、端口和客户端完成公网 HY2 互通验收；不自动重启已有节点或覆盖现有配置。

后续修订已停用 Tunnel、TUN、WireGuard、AmneziaWG、MTProto 本地服务运行时；历史服务文件保留但不再加载。

---

# v0.1.0-core-upgrade.19：补全 VLESS 与 Hysteria2 高级参数

本版本补全原版 Xboard 适配所需的 VLESS Encryption/MLDSA65 字段，以及 Hysteria2 Gecko、UDP Mask、QUIC 和 Masquerade 配置，并更新一键安装脚本。

- VLESS 表单增加协议加密模式、票据、填充、服务端私钥/Seed、MLDSA65 Seed 和 Verify，并提供自动生成 X25519 私钥按钮。
- Hysteria2 表单增加 Salamander/Gecko experimental、伪装密码、Packet Min/Max、QUIC 初始包和 Path MTU、UDP 空闲超时、Masquerade 配置。
- 节点 API、Sing Hysteria2 入站和原版 Xboard 配置校验同步扩展；不支持的字段不会静默丢弃。
- 一键安装默认固定到本版本，Xboard 原机器详情中的 V2bX 安装命令同步更新。

---

# v0.1.0-core-upgrade.18：接回 Xboard 原生管理流程

本版本为配套原版 Xboard 界面适配的预发布版，保留 `.17` 的内核、协议和依赖版本。

- 一键安装包携带 Xboard 机器服务同步组件，新增 `v2bx panel-agent --config /etc/V2bX/xboard-agent.json`。
- 同步优先使用原机器资源下的 `/api/v2/server/machine/services/*`，旧面板接口仅在 404 时回退；鉴权失败不会回退。
- 服务配置、Peer、实际应用状态和批次计量继续复用既有逻辑，不引入节点端网页或额外登录。
- 管理组件来自同一个 SHA256 校验后的发布包；旧版发布包缺少同步组件时仍允许显式回退安装。
- 配套 Xboard 修改位于 `shmily2-1/Xboard`：扩展协议在原节点编辑表单，服务和 Peer 在原服务器详情，配置导出在原用户订阅卡片。

安装或更新只替换组件，不自动重启现有进程。面板原有数据、用户和权限体系保留；生产环境的 Redis/Octane、远程存储及公网业务仍需按部署环境验收。

具体配置见 [Xboard 原生适配](XBOARD-NATIVE.md)。

---

# v0.1.0-core-upgrade.17：扩展协议、本地服务与 Peer 管理

本版本为预发布验收版，包含 P0–P5 协议适配和配套 Xboard 交互能力；保留现有锁定内核与依赖版本。

- 补全 TLS/ECH、REALITY、传输选项和配置校验，修复用户增删、热更新、连接计量及任务退出行为。
- 接入 HTTP/Mixed 独立用户凭据、撤销、计量及客户端导出；支持 VLESS/XDRIVE、MASQUE H2/H3。
- 新增 Tunnel/TUN、WireGuard Peer 管理、持久化地址分配、原生流量计量及私有客户端配置导出。
- 修复启动与 CLI 新增服务并发时的配置快照竞态，确定性回归覆盖新增服务在下一轮被正确应用。此前 `.16` 标签的云端复验拦截了此问题，未发布其安装包。
- 增加独立 AmneziaWG、MTProto 后端适配、进程/健康状态检查和按 UID 计量。
- 配套 Xboard 流量批次确认与恢复，增加服务应用代次和 WireGuard 私钥文件生成功能。原版 Xboard 不自动获得新管理界面，需另行应用面板适配。
- 一键入口、适配安装器和管理器统一固定本标签，从同一 SHA256 校验包更新。已有配置、证书、Peer 地址及计量状态保留；更新不自动重启进程。

验证覆盖 Go 回归、回环真实数据面、独立 Linux 网络命名空间服务测试和安装器回归；云端 CI 通过后才生成四个平台构建包。本次不部署生产节点、不申请公网证书。

AmneziaWG / MTProto 后端程序需单独准备；TUN 需要 Linux 与相应网络权限。安装器不自动部署外部后端或设置宿主 NAT，XDRIVE 公网存储互通和生产长期负载仍需按实际环境验收。

配置与边界见 [P0/P1 说明](PROTOCOL-ADAPTATION-P0-P1.md)、[P2–P5 说明](PROTOCOL-ADAPTATION-P2-P5.md)和[安装指南](INSTALL.md)。

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

---

# v0.1.0-core-upgrade.15：REALITY 客户端握手兼容修复

内嵌 sing-box 不再无条件删除完整的 X25519MLKEM768 曲线和 key share；不完整配对仍会清理，保留原有独立 X25519 认证密钥。修复作用于 V2bX 内的 REALITY 出站，不会替外部手机/路由器客户端更新内核。

- 不降级 Xray，不修改服务端 ClientHello 检查、UUID 认证或版本限制，不关闭证书验证。
- 增加 ClientHello 与 Xray/sing 回环代理测试，覆盖经典/PQ 目标及错误 UUID 拒绝；原 HY2 回归继续保留。
- 记录补丁哈希，CI 校验内嵌源码与官方模块的差异。
- 使用回环地址和临时测试证书验证，没有部署服务器、请求公网 CA 或修改生产面板。

详细证据、客户端限制与排查方法见 [REALITY-HANDSHAKE.md](REALITY-HANDSHAKE.md)。

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

---

# v0.1.0-core-upgrade.14：节点管理菜单

新增菜单18“新增节点”、菜单19“节点管理”和菜单20“移除节点”。新增和修改节点会真实校验面板、证书并重新部署；移除节点会保留剩余节点并重新部署，证书文件保留。

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

---

# v0.1.0-core-upgrade.13：默认一键配置支持多节点

默认配置向导现在可在一次流程中连续填写多个节点；每个节点完成面板识别及证书准备后询问是否继续（Y/N），选择 N 后统一校验、写入并部署整批节点，同时保留现有节点和核心配置。

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

---

# v0.1.0-core-upgrade.12：sing Hysteria2 伪装站点地址

sing 核心的 Hysteria2 一键配置现在可以选择设置 HTTP(S) 伪装站点地址，并将其写入 sing-box `masquerade` 反向代理配置。留空时关闭新设置或保留已有值；输入 `-` 可清除现有地址。此项不改变 TLS 证书域名。

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

---

# v0.1.0-core-upgrade.11：修复 GitHub Actions 非 root 安装器回归

这是 prerelease / 验收版。修复系统工具在无 root 的 CI 回归测试中提前创建 `/var/backups/V2bX-2` 导致的失败；防火墙后端检查通过后才创建备份目录。

## 使用

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

---

# v0.1.0-core-upgrade.10：预发布内核更新与无二次确认安装流程

这是 prerelease / 验收版。节点内核更新至 Xray 26.11.9、sing-box 1.15.0-alpha.13、Hysteria 2 2.13.0；安装与管理脚本更新为免二次确认流程。

## 本次变化

- 首次安装、菜单1、菜单15和 `v2bx generate` 默认只询问四项信息。
- 自动识别面板协议、读取域名/监听端口/用户/跳跃范围，自动合并配置、复用或申请证书、启动节点并设置自启。
- 去掉默认流程中的 HTTP 面板确认、协议选择、证书域名/方式/邮箱、Y/N多节点和DEPLOY提示。
- 同一面板和节点ID更新时保留原调优字段、证书、ACME配置及其他节点；不同节点自动追加。
- 原完整配置向导保留为 `v2bx generate --advanced`。
- 安装脚本减少重复提示；实际错误、回滚和失败退出码保留。
- 菜单卸载、BBR/内核安装、全端口放行、配置覆盖、证书部署和TCP80释放均直接执行，不再要求二次确认。
- 升级 sing-box 内嵌源码并迁移动态用户补丁，更新 Xray/Hysteria 模块与核心API适配。
- 新增四项流程、增量合并、证书路径、协议兼容及自动部署回归；120项本地/隔离测试通过。

## 使用

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/main/install.sh && bash install.sh
~~~

安装后只填写：核心、面板地址、面板密钥、节点ID。需要完整手动向导时运行 `v2bx generate --advanced`。

## 边界

- 新证书默认HTTP-01，域名必须正确解析且TCP80可达；已有证书优先复用。
- REALITY或无TLS不申请公共证书。
- TCP80独立进程会通过pidfd执行TERM/KILL；这类进程不会自动恢复，存在服务中断风险。
- 本次未连接生产面板、申请公共证书或更新服务器。

---

# v0.1.0-core-upgrade.8：HTTP-01 TCP80占用处理与已有证书复用

这是 prerelease / 验收版。Go数据面与三套内核版本保持 .7 不变；不移动旧标签。

## 本次修复

- 重新配置同一面板、节点ID/协议和域名时，校验并复用已有证书及ACME配置，不再仅因向导使用新目录而重复申请证书。
- HTTP-01需要TCP80时，检测IPv4/IPv6占用并显示PID、程序名、服务名；不输出进程命令行参数。
- 托管服务可选择Y确认临时释放，部署成功、失败或正常中断后恢复；不取消服务自启，不修改防火墙。最终DEPLOY前不停止任何服务。
- 独立进程须额外输入KILL-PORT80：使用pidfd先TERM，5秒后仍占用才KILL。无法自动恢复此类进程；不误杀PID复用或新占用者。
- 新增--release-http-port和--kill-http-port显式参数。仅--yes不授权停止其他程序，--check-only绝不执行释放。
- 容器、激活型服务、受保护系统服务及不支持安全pidfd的环境拒绝强制处理。服务恢复失败会返回错误并打印人工恢复命令。
- 逻辑放在既有provision.py管理组件内，保持 .7 安装器更新 .8 的文件清单兼容。
- 新增授权/取消、部署回滚、IPv6、PID变化、TERM/KILL及证书复用回归；CI在隔离netns执行真实TCP80占用进程终止验证。

## 更新与使用

~~~bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.8/install.sh && bash install.sh
v2bx
~~~

旧菜单先17退出再重新打开。菜单1或15进入在线指引，节点仍然一次填写一个，Y继续/N结束。已有正常节点仅更新组件不会自动重新配置或重签证书。

## 安全边界

- 临时停止网站服务会造成短暂中断；独立进程强制终止不能自动恢复，确认前必须核对占用者。
- SIGKILL/断电不能保证运行finally，按屏幕输出的systemctl start命令恢复原服务。
- 强制释放授权仅用于本次部署，**不授权自动续签将来强杀其他服务**。服务恢复后若长期占用80，HTTP续签仍会冲突，建议DNS-01。
- 全部生产配置与证书不为回归测试删除；不循环触发公共CA限流。完全卸载后的证书重新签发需求不变。
- 详细行为见docs/ONLINE-PROVISION.md。隔离测试不等于全部生产环境的长期稳定性保证。
