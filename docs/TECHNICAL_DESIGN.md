# Technical Design / 技术方案

## 总体架构

```text
Agent on VPS  ----HTTPS JSON---->  Controller API  ----SQLite----> Web UI
      |                                  |
      |                                  +--> Public API
      |                                  +--> Admin API
      |                                  +--> Telegram notification dispatch
      +--> local collectors
      +--> tcping / ping / http_get probes
```

Zeno 是全新实现，不兼容 Kulin / Nezha / Komari 的 API、数据库、Agent 协议或安装方式。

## Controller

Go 单二进制，当前职责：

- Agent 鉴权。
- 节点注册 / 管理。
- 接收 heartbeat、host、state、probe results 上报。
- 计算 public status：`online`、`offline`、`warning`、`no_data`。
- 计算月流量 delta。
- 写 SQLite。
- 提供 Public API 给前端。
- 提供 Admin API 给后台。
- 执行 Telegram 通知 dispatch。
- 提供 Agent binary 下载和 install command 生成。
- 执行通知类型 evaluator，维护内部规则命中状态。

Controller 不暴露 Agent token、admin token hash、通知凭据或 bearer secret。即使 Admin API 已鉴权，响应也必须走 explicit DTO。

## Agent

Go 单二进制，当前职责：

- 保存本机 node_id / token / controller_url。
- 周期上报 heartbeat。
- 低频上报 host info。
- 高频上报 state。
- 采集 CPU、内存、磁盘、网络、uptime、load、swap、进程数、TCP 连接数。
- best-effort 自动识别公网 IPv4 / IPv6 / GeoIP 国家码，并随 host report 上报。
- 拉取 probe targets。
- 按每个 target 的 `interval_sec` 调度探测。
- 执行 `tcping`、`ping`/ICMP、`http_get` 多样本探测。
- 上报 probe results。
- 后续可以加入本地 cache/replay。

Agent 不做远控，不接受命令执行。

## Web

Vite + React + TypeScript。

当前页面：

1. 前台主页：服务器卡片、流量/资源概览、延迟摘要、外观设置应用；不单独展示监控服务列表。
2. 节点详情页：延迟目标按钮、延迟图、资源历史图；资源历史包含 CPU、内存、磁盘、网络速率、系统负载、Swap、进程/TCP 连接和网络累计。
3. 服务详情页：同一监控服务在所有节点上的历史延迟曲线。
4. Admin 后台：单管理员登录、账户页修改账号/密码、退出登录、服务器、延迟监控、通知和外观设置；通知页只保留通知渠道和已添加通知类型。
5. Admin 管理动作：服务器创建/编辑/安装命令复制、Agent 接入 URL、目标创建/编辑/删除/排序/分配、通知渠道、通知类型添加/移除/编辑和作用范围。

UI 规则：保持已确认主页卡片、详情页密度和 Admin 分区结构；后台参考 Kulin 的清爽布局，但使用 Zeno 自己的视觉语言，不恢复旧介绍区。

## Admin 认证

- 单管理员账号默认是 `admin`，可在后台“账户”页修改账号名。
- 首次部署未设置 `admin_username` / `admin_password_hash` 时，bootstrap admin token 可作为登录密码；修改账号或密码后以 DB 中设置为准，旧 bootstrap token 不再作为后台 API 凭据。
- 登录成功返回 opaque session token，后续 Admin API 仍用 `X-Admin-Token`，但值是 session token。
- 修改密码会轮换 session 并清空旧 session；退出登录删除当前 session。
- 登录失败做内存限速，避免暴力尝试。

## 在线状态计算

Controller 根据 `last_seen_at`、离线通知规则的 `duration_sec` 和资源规则命中状态计算。离线通知默认 60 秒，公共页面和通知补扫使用同一个超时时间：

```text
online: last_seen_at 未超过离线通知 duration，且无未恢复的 resource warning
warning: 最近有 heartbeat，但资源规则命中异常
offline: last_seen_at 超过离线通知 duration
no_data: 从未收到 heartbeat/state
```

关键规则：heartbeat/host 只证明 Agent 活着，不能清除 resource warning；新的 state 上报会更新资源规则命中状态；服务探测结果只记录目标质量，不拥有节点在线状态或 `last_seen_at`。presence WebSocket 只用于配置变更推送和触发补扫，不覆盖公共页面或后台列表的在线状态。Controller 启动后先等待一个完整心跳窗口，再周期性扫描 stale `last_seen_at` 并原子落库离线状态，避免部署重启误报以及 WebSocket 断开事件丢失导致漏发离线通知；恢复通知会对照持久化 incident state/mark 补发并清理残留，同时拒绝没有 active incident 的 recovery-only 通知。

## 月流量计算

Agent 上报累计 counter：

```text
net_in_total_bytes
net_out_total_bytes
```

Controller 计算 delta：

```text
delta_in = current_in_total - last_in_total
delta_out = current_out_total - last_out_total
```

规则：

- 首次 sample 只建立 baseline。
- delta < 0：counter reset，更新 baseline，不计入。
- delta 过大：丢弃或标记异常，避免脏数据污染月流量。
- billing mode：
  - `in`: billable = in_bytes
  - `out`: billable = out_bytes
  - `both`: billable = in_bytes + out_bytes
  - `max`: billable = max(in_bytes, out_bytes)
- `monthly_reset_day`：每台服务器可设置月流量重置日。重置日为 1 时按自然月；重置日大于 1 时，重置日前的样本计入上一个账单周期；月末不存在该日期时按当月最后一天计算。
- Public summary 会返回当前流量计费周期的 `monthly_period_start` / `monthly_period_end`，首页流量条直接展示这段周期范围，避免“本月”口径不清。

## 续费金额与汇率

- `nodes` 保存原始 `renewal_amount`、`renewal_currency` 和 `billing_cycle`；Public summary 同时返回完整的 CNY-per-unit 汇率快照。
- Controller 并发读取 Google Finance 的各币种/CNY 页面，提取当天的直接报价；启动时刷新一次，之后每 24 小时刷新。
- 汇率通过 HTTPS 获取并完整校验后，在同一 SQLite 事务中写入 `exchange_rates`；任何币种缺失时整批拒绝，保留上次成功缓存。
- Public summary 按账单周期折算 `monthly_cost_cny`；永久节点、未配置金额、未知周期或无可用汇率时不产生月均消费。
- 首页金额单位默认 CNY，并将选择保存在浏览器；顶部月均消费从 CNY 换算，服务器卡片从节点原始币种换算，避免多次换算产生累计误差。

## 延迟 / 服务探测数据

每轮保留 summary + samples：

- summary 用于快速首页、服务详情定位和图表。
- samples 保留 raw ping/tcp/http 结果，便于后续扩展抖动、尖峰、loss 细节。
- 原始状态和探测历史最多保留 30 天，由 Controller 启动后及每小时定期清理。
- 7 天、30 天历史只允许已登录管理员读取；游客仍可读取实时和 1 天范围。

目标类型：

- `tcping`: TCP connect，必须有 port。
- `ping`: ICMP，不使用 port。
- `http_get`: HTTP/HTTPS GET，不使用 port；延迟统计到响应头返回为止，不下载或校验响应体；2xx/3xx 成功，4xx/5xx 作为 unhealthy 状态失败。

不要只存 avg。

Public summary 会返回 `services`，按后台探针目标显示顺序列出有效目标；目标本身没有全局启用/停用状态，具体节点是否探测由关联配置控制。服务详情接口 `GET /api/public/v1/services/{target_id}/latency` 把同一目标按节点分线返回，前端直接复用延迟图表。

## 通知

当前通知模型保持简单：

- 渠道：Telegram-only。
- 事件：`node_offline`、`probe_unhealthy`、`renewal_due`、手动 `test_notification`。
- 对账模型：每个（渠道、节点、类别）只保存已告诉用户的状态 `notified`，真实状态每轮从已存储的 `nodes.status`、资源规则命中状态和续费规则读取；单个对账 goroutine 每 2 秒一轮，状态写入提交后非阻塞唤醒。不一致时按当前状态生成一条消息，发送成功才推进 `notified`，任何失败都下一轮重试（按渠道退避，429 按 `retry_after`）；恢复需持续 `MAX(duration_sec)`；告警没发出去而状态已回落时只记 `dropped` 日志。首次见到时静默基线。
- 通知发送不在 Agent 请求路径上，不能阻塞 Agent 上报；发送期间不持有数据库事务。
- Admin 手动测试发送同步返回本次 sanitized 结果，方便操作员立即验证配置。

## 通知类型

通知类型规则持久化为 `alert_rules`，Admin 文案统一放在“通知”下；后台只展示已启用/已添加的通知类型，未启用的预置规则通过“添加通知类型”弹窗选择。

当前规则覆盖：

- CPU 使用率。
- 内存使用率。
- 磁盘使用率。
- 离线通知。

规则支持：

- `enabled`。
- `threshold` / `threshold_unit`。
- `duration_sec`：资源规则表示统计窗口；离线规则表示心跳超时时间，公共状态和离线通知共用。
- `scope_node_ids`：为空表示全部服务器；非空时只作用于指定服务器。

`alert_rule_states` 用于 Controller 内部合并不同规则命中状态，避免某一类健康上报误清另一类仍活跃状态。

## 公网 IP / GeoIP

Agent 使用 tokenless、可替换的轻量 HTTP provider 自动发现公网 IPv4、IPv6 和国家码，并通过 `/api/agent/v1/host` 的 `public_ipv4`、`public_ipv6`、`country_code` 上报。

原则：

- 识别结果缓存，默认 6 小时刷新一次，避免每分钟心跳都访问外部 provider。
- IPv6 不可用时只上报 IPv4；IPv4 / IPv6 / GeoIP 任一失败都不影响 heartbeat、state 或 probe 上报。
- Controller 只用非空且合法的字段更新 `nodes.public_ipv4`、`nodes.public_ipv6`、`nodes.country_code`。
- Agent 省略字段或 provider 失败时，Controller 保留后台已有值，不清空。
- 不写死外部 token，不把 provider token 暴露给 Admin API。

## 设置 / 外观

设置保存在通用 `settings` 表中。

当前公开展示字段：

- `site_title`
- `logo_url`
- `theme`
- `desktop_background_url`
- `mobile_background_url`
- `appearance_preset`
- `card_opacity`
- `card_blur`
- `card_radius`
- `border_strength`
- `shadow_strength`
- `background_overlay`
- `theme_color`
- `custom_code`（CSS-only 外观扩展；前端不执行脚本/事件处理器）

兼容字段：`background_url` 会映射到 desktop background。不要重新拆出 `avatar_url`。

## 部署

Controller 使用 Docker Compose 安装器部署：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/zeno.db
/opt/zeno/secrets/
/opt/zeno/backups/
127.0.0.1:18980
```

`install.sh` 负责镜像 provenance 校验、停服前预检、一致性备份、SQLite 检查、配置替换、readiness 和失败恢复。Agent 二进制和服务由独立 Zeno-Agent 项目发布。`scripts/import-guko-servers.py` 可把 GUKO `server-manager/servers.json` 导入 Zeno Admin nodes，只同步展示元数据，不删除节点、不轮换 Agent token。

安全更新顺序：

1. 本地测试和镜像构建门禁。
2. 用明确 SemVer tag 或 digest 运行安装器。
3. 安装器验证目标镜像并在停旧容器前完成路径和空间预检。
4. 停旧容器，创建完整离线备份并执行 SQLite `quick_check`。
5. 原子替换 Compose 配置并启动 Controller。
6. 等待 `/ready`，然后 smoke Admin/Public API 和 Agent 上报。
7. readiness 失败时从完整备份和固定旧镜像恢复。

## 下一步设计重点

1. 多节点铺 Agent：先确认 Controller 公网 HTTPS 入口，再小批量安装和 smoke。
2. UI polish：拖拽排序等。
