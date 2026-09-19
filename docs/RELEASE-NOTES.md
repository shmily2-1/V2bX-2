# v0.1.0-core-upgrade.4：在线配置 / 自动证书 / 完整菜单

这是 prerelease / 验收版。保留.2三内核版本与Hysteria2端口跳跃：Xray26.3.27、sing-box1.14.1兼容层、Hysteria2 core/extras2.12.3。

## 本次变化

- 菜单15不再仅生成JSON：实际读取Xboard节点/授权用户，识别协议/监听端口，预检域名/证书，确认后停止旧服务、备份写入、通过内置lego申请Let's Encrypt并启动/设置自启，检查实际PID/监听/证书。失败回滚，避免反复重启导致CA限流。
- 菜单0私有副本编辑后走在线预检；--offline和--root保留离线、不操作宿主服务的边界。
- 恢复0–17原编号及真实systemd状态；8日志、9自启、10取消自启、11BBR、16全部端口放行。
- BBR只使用发行版已配置签名源的内核候选包，两次确认，不自动重启/删除旧内核。全端口临时开放有危险确认及120秒自动撤回，保留NAT/其它规则；复杂防火墙管理器会拒绝自动处理。
- Hysteria2空用户可安全启动等待；HTTP200空数组会撤销全部用户，304不误清空。其它协议仍需授权用户才能首次启动。新增真实回环空用户认证拒绝测试。
- 证书/私钥匹配、域名和有效期检查；ACME账户及私钥0600，坏PEM不会panic，续期解析错误不再吞掉。
- 新增在线配置与失败回滚测试，打包/安装/维护脚本均来自同一SHA256验证的Release。

## 一键安装

以root在Linux amd64/arm64 + systemd执行：

```bash
wget -N https://raw.githubusercontent.com/shmily2-1/V2bX-2/v0.1.0-core-upgrade.4/install.sh && bash install.sh
v2bx generate
```

普通安装保留已有配置、证书、服务，不自动重启旧进程；只有在线配置中确认DEPLOY才替换配置/启动。旧节点请先备份，在维护窗口更新并检查实际客户端。

在线部署的HTTP面板需要显式风险确认；--from-file文件须root/0600，密钥不放命令行。HTTP-01要公网TCP80，与节点UDP443不同。Hysteria2跳跃还需面板补丁、nft/iptables及UDP范围云安全组。

细节及边界见 docs/ONLINE-PROVISION.md、docs/INSTALL.md。BBR内核升级、全端口开放不是节点上线的必需步骤，不在普通安装或配置中自动执行。
