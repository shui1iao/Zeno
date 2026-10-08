# Security / 安全边界

Zeno 的原则是 **Agent 只采集和上报，不接受远程命令**。本页说明当前版本的实际边界；它不是“把 Admin 暴露到公网也安全”的承诺。

## 私密报告安全漏洞

请使用 GitHub 的 **Private vulnerability reporting**：

<https://github.com/shui1iao/Zeno/security/advisories/new>

维护者目标是在 7 天内确认收到、14 天内给出初步判断，并与报告者协调修复和披露时间。复杂度与可复现性可能影响时限；修复和 advisory 准备好之前请勿公开（法律强制披露除外）。安全修复面向最新稳定 Controller 与 [COMPATIBILITY.md](COMPATIBILITY.md) 中列为支持的 Agent 组合。

不要为未修复漏洞开公开 Issue，也不要在 Issue、讨论、截图或日志中粘贴 Agent/Admin/session/enrollment token、完整安装命令、`Authorization` header、Telegram Bot Token、数据库或 `secrets/`。若私密报告入口不可用，只在公开 Issue 中说明“需要私密联系维护者”，不要附漏洞复现细节或任何凭据。

## 身份、会话与凭据

### Agent

- 每台节点使用独立 runtime token；Controller 只保存 token hash。
- 后台生成的 enrollment token 是短期、一次性凭据。安装器兑换成功后在本机生成/保存 runtime token；不要重复发布完整安装命令。
- token 只通过 `Authorization: Bearer` 发送，不进入 URL query、Admin API 响应或正常日志。
- 重新生成接入命令可能使旧凭据失效。疑似泄露时重新生成、重装该节点并确认旧 Agent 无法上报。

### Admin

- 首次安装的 bootstrap token 位于 `/opt/zeno/secrets/zeno_admin_token`，只用于初始化/恢复入口；完成账号设置后使用用户名、密码和短期 session 登录。
- Admin 登录有速率限制；反向代理来源地址只有在 `ZENO_TRUSTED_PROXIES` 中才被信任。不要把任意 RFC1918 网段或公网来源整体设为 trusted proxy。
- Admin/session token 不进入 URL。退出登录、修改密码或怀疑泄露时应使旧 session 失效并重新登录。
- Admin API 拥有创建节点、生成 enrollment、修改探测与通知等高权限；必须通过 HTTPS 保护，不能依靠“URL 不公开”作为认证。

## 无远程执行

Zeno 不提供 command exec、shell、文件管理、脚本执行或通用任务下发。Controller 下发给 Agent 的内容仅为受结构约束的探测配置：类型、目标、端口、次数、超时、间隔和分配范围。Agent 通过 presence WebSocket 接收“配置变化”通知，随后主动使用鉴权 HTTP API 拉取完整配置。

## 探测能力与 SSRF 边界

当前支持：

- ICMP Ping；
- TCP Ping（必须是单个显式端口，不支持端口范围）；
- HTTP GET（不支持自定义 method/body/认证 header）。

次数、超时、间隔和单轮预算都有服务端上下限；选项形式的主机地址、非法端口、未知类型和超预算配置会被拒绝。HTTP URL 禁止内嵌用户名/密码，限制 redirect 次数，并拒绝 HTTPS 跳转降级到 HTTP。

**重要：Admin 本身是受信任的监控配置者。** 为监控内网服务，HTTPS 目标和 loopback HTTP 可以访问私网；“直接 IP + 显式端口”的 HTTP 也允许。因此 Zeno 不是面向不可信租户的 SSRF 沙箱。不要向不可信用户授予 Admin 权限；在高隔离环境中应另外用容器网络/出口 ACL 限制 Agent 和 Controller 可达地址。

## API、WebSocket 与公开页面

- Public API/公开页面只返回展示所需的节点状态、资源百分比、延迟摘要和聚合流量，不返回 token hash、原始凭据、本机路径或完整私有配置。
- Admin API 和 Admin WebSocket 必须有有效 session；Agent API/presence WebSocket 必须有对应节点的有效凭据。不要在反向代理中删除或伪造鉴权边界。
- Controller 根据可信代理配置解析来源与 HTTPS 状态；错误配置 `X-Forwarded-*` 信任会削弱限流和安全响应头判断。

## 通知凭据、keyring 与 authority

- Admin 创建/更新 Telegram 渠道时可提交 Bot Token；后续读取只返回 `credential_set`，不回显原文。编辑时 credential 留空表示保留旧值。
- Telegram Bot Token 不进入通知正文、payload、测试结果或日志；失败消息应保持脱敏。通知失败不阻塞 Agent 数据写入。
- 渠道凭据使用外部 32-byte credential keyring 加密；SQLite 保存 ciphertext、key id/fingerprint，不保存 authority key。
- authority keyring 验证数据库绑定；轮换时先部署新旧双 key，将 `active_key_id` 切到新 key、完成改写后再删除旧 key。不要只恢复数据库而遗漏与之匹配的两个 keyring。
- 官方安装的 keyring 文件位于 `secrets/`，必须为普通文件、root 持有且不得向 group/other 开放写权限。Compose 以只读 secret mount 提供给非 root Controller。
- 测试渠道也会参与通知对账，可能有待送达的消息（`notification_states.pending_target`）并在换入真实凭据后发出。删除/禁用测试规则和渠道后，才可换入真实凭据；不要用生产 Bot Token 做验收。

## 公网部署 checklist

- 官方 Compose 保持 `127.0.0.1:18980` loopback 绑定；不要添加 `0.0.0.0:18980` 公网映射。
- 使用 Caddy/Nginx/等 HTTPS 反代；正确转发 WebSocket upgrade，并只配置实际反代地址到 `ZENO_TRUSTED_PROXIES`。
- 远程 Agent 默认使用 HTTPS。远程明文 HTTP 只允许“直接 IP + 显式端口”且安装/runtime 都显式 opt-in；bearer token 会明文传输，不适合公网。
- Controller 以 UID/GID `10001:10001` 非 root 运行，root filesystem 只读、drop all capabilities、`no-new-privileges`。不要为排障长期放宽这些限制。
- `/opt/zeno/data` 应为 `10001:10001` 且目录 `0700`/文件 `0600`；`secrets` 应为 `root:10001`、目录 `0750`/文件 `0640`。
- 不公开数据库、备份、`.env`、安装日志或含节点真实地址的截图。

## 备份、升级与恢复

完整备份范围：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/
/opt/zeno/secrets/
```

安全安装器在修改既有安装前创建一致性离线备份，写入完整性 marker 和 SHA-256 manifest，并对 SQLite 副本与当前库执行 `quick_check`。启动失败时会固定旧 image ID 并自动恢复完整备份。手工恢复前必须停止 Controller；不要覆盖运行中的 SQLite，也不要混用不同时间点的数据库与 notification keyring/authority。

备份包含等同生产权限的 secret：加密存储、限制读取、定期演练恢复，并按 [UPGRADE.md](UPGRADE.md) 清理过期备份。兼容与弃用范围见 [COMPATIBILITY.md](COMPATIBILITY.md)。

## 日志与报告红线

任何日志、Issue、截图、验收报告中都不得出现：

- Agent runtime/enrollment token、Admin bootstrap/session token；
- 完整安装命令或 `Authorization` header；
- Telegram Bot Token、notification keyring/authority key；
- 未脱敏数据库、`.env` 或备份内容。

只记录状态码、错误类别、版本、时间窗和不可逆 hash；hash 也不要用于低熵密码或短 token。

## 历史公开信息

早期已发布源码中的 preview seed 曾包含维护者真实服务器名称、IP 与端口。这些值已经从当前 tree 替换为 RFC 5737/3849 文档数据；为避免破坏已发布 tag 的完整性，不重写 Git 历史。相关地址应视为已经公开过，不能把删除当前文件误认为秘密轮换或访问控制修复。运维方应自行评估并轮换真正的凭据；端口是否变更由服务器所有者独立决定，不属于本仓库代码修复。

## English summary

Report vulnerabilities privately at <https://github.com/shui1iao/Zeno/security/advisories/new>. The response targets are acknowledgement within 7 days and an initial assessment within 14 days; coordinate disclosure. Security fixes cover the latest stable Controller and combinations listed in [COMPATIBILITY.md](COMPATIBILITY.md).

Agents have independent runtime credentials; one-time enrollment expires after 10 minutes. Generating another command revokes the previous unused enrollment without interrupting the active runtime token. Admin, Agent, notification, and keyring credentials must never enter URLs, logs, screenshots, Issues, or backups without encryption/access controls. Keep the Controller loopback-only behind HTTPS/WebSocket proxying and narrowly configure trusted proxies. Admin is trusted to configure probes; Zeno is not a multi-tenant SSRF sandbox. Back up/restore the database and both notification keyrings together.

Older published preview seed history contained real maintainer infrastructure names, addresses, and a port. The current tree uses RFC documentation data. Published tags are not rewritten, so the historical values remain public; deleting them from the current tree is not credential rotation or an access-control change.
