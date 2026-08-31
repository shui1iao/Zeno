# Zeno API

Zeno API 全新设计，不兼容旧系统。

## 认证约定

Agent 请求头：

```http
Authorization: Bearer <agent-token>
X-Node-ID: <node-id>
X-Agent-Version: <version>
```

安全要求：

- token 不放 query string。
- Controller 只存 token hash。
- 认证失败返回 401。
- disabled node 返回 403。

Admin 登录：

- `POST /api/admin/v1/login` 使用单管理员账号（默认 `admin`，可在后台账户页修改）+ 密码换取 opaque session token。
- 后续 Admin API 继续通过 `X-Admin-Token: <session-token>` 调用；兼容首次部署时的 bootstrap admin token。
- `GET /api/admin/v1/account` 返回当前单管理员账号。
- `POST /api/admin/v1/account` 修改账号和可选新密码，会轮换 session；改过账号或密码后，旧 bootstrap token 不再作为后台 API 凭据使用。
- `POST /api/admin/v1/logout` 注销当前 session。
- 登录失败有内存限速，避免暴力尝试。

### POST /api/admin/v1/login

```json
{
  "username": "admin",
  "password": "current-password"
}
```

响应：

```json
{
  "username": "admin",
  "token": "opaque-session-token"
}
```

### GET /api/admin/v1/account

请求头：

```http
X-Admin-Token: <session-token>
```

响应：

```json
{
  "account": {
    "username": "admin"
  }
}
```

### POST /api/admin/v1/account

请求头：

```http
X-Admin-Token: <session-token>
```

请求：

```json
{
  "username": "admin",
  "current_password": "current-password",
  "new_password": "new-password-or-empty"
}
```

响应同登录，会返回新的 session token。`new_password` 留空时只修改账号。

### POST /api/admin/v1/logout

请求头：

```http
X-Admin-Token: <session-token>
```

成功返回 `204`。

## Agent API

### POST /api/agent/v1/heartbeat

请求：

```json
{
  "now": 1782990000
}
```

响应：

```json
{
  "ok": true,
  "server_time": 1782990000
}
```

### POST /api/agent/v1/host

请求：

```json
{
  "hostname": "example-node-a",
  "os_name": "Debian",
  "os_version": "13",
  "kernel": "6.12.x",
  "arch": "x86_64",
  "virtualization": "kvm",
  "cpu_model": "AMD EPYC",
  "cpu_cores": 2,
  "memory_total_bytes": 2147483648,
  "disk_total_bytes": 42949672960,
  "boot_time": 1782900000,
  "agent_version": "0.1.0",
  "public_ipv4": "198.51.100.8",
  "public_ipv6": "2001:db8::8",
  "country_code": "JP"
}
```

`public_ipv4`、`public_ipv6`、`country_code` 由 Agent 轻量自动识别后 best-effort 上报。字段可省略；Controller 只用非空且合法的值更新节点元数据，识别失败不会清空后台已有 IPv4 / IPv6 / 国家码。

### POST /api/agent/v1/state

请求：

```json
{
  "ts": 1782990000,
  "cpu_percent": 12.3,
  "load1": 0.42,
  "load5": 0.35,
  "load15": 0.28,
  "memory_used_bytes": 751619276,
  "memory_total_bytes": 2147483648,
  "swap_used_bytes": 268435456,
  "swap_total_bytes": 1073741824,
  "disk_used_bytes": 8589934592,
  "disk_total_bytes": 42949672960,
  "net_in_total_bytes": 123456789,
  "net_out_total_bytes": 987654321,
  "net_in_speed_bps": 10240,
  "net_out_speed_bps": 20480,
  "process_count": 88,
  "tcp_connection_count": 34,
  "uptime_seconds": 86400
}
```

`load1` / `load5` / `load15`、`swap_*`、`process_count`、`tcp_connection_count` 是新 Agent 上报字段；旧 Agent 省略时会按 `null` 存储/展示，不会伪装成 0。`tcp_connection_count` 统计 `/proc/net/tcp*` 的连接表数据行，包含监听等 TCP socket 行。

### GET /api/agent/v1/probe-targets

响应：

```json
{
  "version": 12,
  "targets": [
    {
      "id": "google-dns",
      "name": "Google DNS",
      "type": "ping",
      "address": "8.8.8.8",
      "count": 20,
      "timeout_ms": 600,
      "interval_sec": 30
    },
    {
      "id": "telegram-dc5",
      "name": "Telegram DC5",
      "type": "tcping",
      "address": "203.0.113.44",
      "port": 443,
      "count": 10,
      "timeout_ms": 600,
      "interval_sec": 30
    },
    {
      "id": "zeno-health",
      "name": "Zeno Health",
      "type": "http_get",
      "address": "https://example.com/health",
      "count": 2,
      "timeout_ms": 600,
      "interval_sec": 30
    }
  ]
}
```

`version` 是 Controller 当前探针配置版本；Admin 对探针目标的新增、编辑、删除会在同一数据库事务里更新配置和递增该版本。Agent 应把本次下发配置对应的 `version` 原样作为 `config_version` 随 `/api/agent/v1/probe-results` 上报。

探针目标类型：

- `tcping`：TCP 连接探测，必须带 `port`。
- `ping`：ICMP ping，不带 `port`。
- `http_get`：HTTP/HTTPS GET 探测，不带 `port` 字段。HTTPS 可使用普通主机名；HTTP 只允许 loopback，或“直接 IP + 显式端口”，并拒绝 URL userinfo。初始 URL、每一跳及最终 URL 执行同一规则；最多跟随 10 跳，任一 HTTPS→HTTP 降级都会失败，跨 origin 时只保留 `User-Agent`、不转发 Referer/认证或自定义请求头。探测延迟统计到响应头返回为止，不下载或校验响应体；2xx/3xx 算成功，4xx/5xx 作为失败样本记录 `http_status_<code>`。

Controller 对下发给单个节点的探针配置做资源上限：最多 32 个启用目标；单目标 `count` 为 1–32，`timeout_ms` 为 100–5000，`interval_sec` 为 5–3600；单目标 `count * timeout_ms` 不超过 60000ms 且不超过 `interval_sec`，单节点一轮总预算不超过 120000ms。迁移前遗留的超大配置会在下发/本地 collector 执行前裁剪到这些范围。

### POST /api/agent/v1/probe-results

请求：

```json
{
  "config_version": 12,
  "rounds": [
    {
      "round_id": "9f8b765f7a0c41a9823f478786e8dbb9",
      "target_id": "google-dns",
      "ts": 1782990000,
      "type": "ping",
      "sent": 20,
      "received": 20,
      "loss_percent": 0,
      "min_ms": 0.51,
      "avg_ms": 0.66,
      "median_ms": 0.63,
      "max_ms": 1.2,
      "stddev_ms": 0.12,
      "samples": [
        {"seq": 1, "success": true, "latency_ms": 0.62},
        {"seq": 2, "success": false, "latency_ms": null, "error": "timeout"}
      ]
    }
  ]
}
```

`config_version` 必须来自最近一次 `/api/agent/v1/probe-targets` 响应的 `version`。滚动升级期间，上一版 Agent 使用的顶层 `version` 仍作为别名接收，并执行相同版本校验；若 `version` 与 `config_version` 同时提供且非零值不同，请求会被拒绝。非零版本上报时，Controller 会在同一个 SQLite 事务边界内比较当前配置版本、读取当前启用目标，并写入整批 round/sample；若配置已变化，返回 `409 Conflict` 和 `{"error":"stale_probe_config"}`，整批结果不会写入任何 round/sample，Agent 应重新拉取探针配置后再上报。两个版本字段均为 `0` 或均省略只用于旧 Agent 兼容：Controller 无法判断旧配置是否过期，因此不做版本比较，但仍会在同一事务里按当前启用目标、类型和 `count` 校验整批数据；任一 round 不符合当前配置时整批拒绝且零写入。

`round_id` 由 Agent 为每轮探测生成，并在重试同一请求时保持不变；Controller 用它去重精确重试，同时允许同一秒内保存不同探测轮次。旧版 Agent 未提供该字段时，Controller 仍使用样本摘要兼容去重。单次上报最多 32 个 round；每个 round 的样本数不能超过该目标下发的 `count`，且硬上限为 32。`error` 按 UTF-8 字节计：单样本最多 512 bytes、单 round 累计最多 4 KiB、单请求累计最多 32 KiB；任一预算超限时整批返回 `400` 且零写入。Store 层重复执行相同预算校验，并对内部调用按单样本/单 round 预算防御性截断，避免 `probe_rounds` / `probe_samples` 重复放大。

## Public API

### GET /api/public/v1/settings

读取公开站点配置。首页启动时会先读取该接口，用于品牌标题、头像/Logo、主题、Agent 接入 URL、电脑端/手机端背景图，以及管理员配置的自定义 CSS。头像/Logo 只用 `logo_url` 一个字段，不再拆出额外头像字段。图片字段只保存 URL / 站内静态路径，不存图片二进制。响应只包含公开展示字段，不包含 Admin token、Agent token、token hash、通知渠道凭据、secret 或 credential 原文。

默认值：

```json
{
  "site_title": "Zeno",
  "logo_url": "/assets/logo/id.png",
  "theme": "system",
  "agent_controller_url": "",
  "background_url": "",
  "desktop_background_url": "",
  "mobile_background_url": "",
  "appearance_preset": "default",
  "card_opacity": 0.7,
  "card_blur": 0,
  "card_radius": 20,
  "border_strength": 0.3,
  "shadow_strength": 0.2,
  "background_overlay": 0,
  "theme_color": "#2563eb",
  "custom_code": ""
}
```

### GET /api/public/v1/summary

首页使用，返回节点卡片所需数据。节点按后台 `display_order ASC, id ASC` 排序；`expiry_label` 来自后台节点的 `expiry_date`。配置续费金额后，节点同时返回原币种金额、账单周期和按最近一次成功汇率折算的人民币月均消费；永久节点不计入月均消费。`exchange_rates` 表示 1 单位币种可兑换的人民币金额，供首页顶部和服务器卡片跟随用户选择的展示单位换算；默认选择 CNY。Controller 每天从 Google Finance 获取各币种兑人民币的当天汇率并持久缓存，刷新失败时继续使用上次成功值。

节点响应示例：

```json
{
  "nodes": [
    {
      "id": "example-node-a",
      "display_name": "Example Node A",
      "status": "online",
      "country_code": "HK",
      "expiry_label": "2026-08-01",
      "renewal_amount": 20,
      "renewal_currency": "USD",
      "billing_cycle": "年",
      "monthly_cost_cny": 11.75,
      "cpu_percent": 12.5,
      "memory_used_bytes": 1073741824,
      "memory_total_bytes": 2147483648,
      "disk_used_bytes": 10737418240,
      "disk_total_bytes": 42949672960,
      "net_in_speed_bps": 1024,
      "net_out_speed_bps": 2048,
      "net_in_total_bytes": 4096,
      "net_out_total_bytes": 8192,
      "net_in_lifetime_bytes": 1099511631872,
      "net_out_lifetime_bytes": 1099511635968,
      "billing_mode": "both",
      "monthly_reset_day": 15,
      "monthly_period_start": "2026-06-15",
      "monthly_period_end": "2026-07-14",
      "monthly_billable_bytes": 1099511627776,
      "monthly_quota_bytes": 2199023255552
    }
  ],
  "services": [
    {
      "id": "google",
      "name": "Google",
      "type": "http_get",
      "assigned_node_count": 10,
      "reporting_node_count": 9,
      "median_ms": 1.2,
      "loss_percent": 0,
      "updated_at": "2026-07-04T12:00:00Z"
    }
  ],
  "latency_points": [],
  "exchange_rates": {
    "CNY": 1,
    "USD": 7.18,
    "EUR": 8.42
  }
}
```

`monthly_period_start` / `monthly_period_end` 是当前流量计费周期的 UTC 日期范围，按该节点 `monthly_reset_day` 计算；`monthly_billable_bytes` 也取同一周期。

`net_in_total_bytes` / `net_out_total_bytes` 保留 Agent 当前网卡 counter，供节点详情和状态历史使用，服务器重启后可能归零。`net_in_lifetime_bytes` / `net_out_lifetime_bytes` 由 Controller 持久化累计：首次有效样本保留当时 counter，之后按 counter delta 累计；服务器、Agent 或网卡重启使 counter 降低时，重置后的较小 counter 会作为重置后已产生的流量计入永久累计，并成为下一次采样的基线。首页顶部“接收 / 发送”使用 lifetime 字段；旧缓存缺少字段时临时回退到 raw counter。

`services` 是公开服务详情页使用的探针目标摘要。它按后台探针目标显示顺序返回有效目标，`assigned_node_count` 是分配且启用的节点数量，`reporting_node_count` 是最近 24 小时内有上报的节点数量，延迟/丢包取该服务最新一条探测结果；公开 DTO 不返回探测地址或端口，完整端点只在管理员接口中可见；前台首页不单独展示监控服务列表。

### GET /api/public/v1/services/{target_id}/latency

查询某个监控服务在所有节点上的历史延迟。前端把每个节点作为一条曲线，用于服务详情页。`range` 支持 `1h`、`1d`、`7d`、`30d`；未登录前台只提供 `1h` / `1d`，登录后才显示 `7d` / `30d`。若后端对长范围返回 401，前端会清除内存中的登录状态并提示重新登录。

```json
{
  "target": {
    "id": "google",
    "name": "Google",
    "type": "http_get",
    "assigned_node_count": 10,
    "reporting_node_count": 9,
    "median_ms": 1.2,
    "loss_percent": 0,
    "updated_at": "2026-07-04T12:00:00Z"
  },
  "range": "1d",
  "created_at": [1783166400000],
  "series": [
    {
      "node_id": "example-node-a",
      "node_name": "Example Node A",
      "median_ms": [1.2],
      "avg_ms": [1.3],
      "loss_percent": [0]
    }
  ]
}
```

历史响应使用紧凑列式格式：`created_at` 是 Unix 毫秒时间数组，`median_ms`、`avg_ms` 与 `loss_percent` 按索引对齐。所有曲线时间轴一致时 `created_at` 位于响应顶层；时间轴不一致时，每个 `series` 项携带自己的 `created_at`。

### GET /api/public/v1/nodes/{node_id}/latency

查询某节点延迟图数据。数据来自 Agent 上报的 probe rounds。

参数：

```text
range=1h|1d|7d|30d
```

未登录前台只显示 `1h` / `1d`；登录后才显示 `7d` / `30d`。若长范围请求返回 401，前端清除本地 Admin token 并提示重新登录。详情页优先用 WS 实时更新，WS 不可用/超时/失败时再发轻量 HTTP fallback，避免一进入页面就 HTTP+WS 重复请求。

响应字段重点：

```json
{
  "node_id": "example-node-a",
  "range": "1h",
  "created_at": [1783041600000],
  "series": [
    {
      "target_id": "google-dns",
      "target_name": "Google DNS",
      "median_ms": [0.8],
      "avg_ms": [0.9],
      "loss_percent": [0]
    }
  ]
}
```

该接口采用与服务历史相同的紧凑列式格式；三个指标数组与共享或曲线内的 `created_at` 按索引对齐。

### GET /api/public/v1/nodes/{node_id}/state

查询某节点 Agent 状态历史，用于后续资源/网络历史图。数据来自 Agent 上报的 `state_samples`。

参数：

```text
range=1h|1d|7d|30d
```

未登录前台只显示 `1h` / `1d`；登录后才显示 `7d` / `30d`。若长范围请求返回 401，前端清除本地 Admin token 并提示重新登录。详情页优先用 WS 实时更新，WS 不可用/超时/失败时再发轻量 HTTP fallback，避免一进入页面就 HTTP+WS 重复请求。

响应字段重点：

```json
{
  "node_id": "example-node-a",
  "range": "1h",
  "points": [
    {
      "ts": "2026-07-03T01:20:00Z",
      "cpu_percent": 18.75,
      "load1": 0.42,
      "load5": 0.35,
      "load15": 0.28,
      "memory_used_bytes": 4294967296,
      "memory_total_bytes": 8589934592,
      "swap_used_bytes": 536870912,
      "swap_total_bytes": 2147483648,
      "disk_used_bytes": 42949672960,
      "disk_total_bytes": 171798691840,
      "net_in_total_bytes": 1000000,
      "net_out_total_bytes": 2000000,
      "net_in_speed_bps": 2048.5,
      "net_out_speed_bps": 1024.25,
      "process_count": 88,
      "tcp_connection_count": 34,
      "uptime_seconds": 3601
    }
  ]
}
```

新指标字段可能为 `null`：旧 Agent 或迁移前历史采样没有对应值时，Public API 保持空值，前端不会显示成 0。`tcp_connection_count` 的语义同 Agent 上报字段：统计 `/proc/net/tcp*` 连接表数据行。

## Admin API

Admin API 第一版只给管理 UI 或 CLI 使用，需要请求头：

```http
X-Admin-Token: <admin-token>
```

安全要求：

- admin token 不放 query string。
- Controller 启动时通过 `-admin-token` 或 `-admin-token-file` 配置，内部只比较 hash。
- Admin API 也必须使用显式 DTO，不能返回 `token_hash`、token 原文或 secret 字段。

### GET /api/admin/v1/performance

返回只读运行时性能信号，用于判断是否真的需要继续优化。该接口不在普通后台界面展示，也不返回请求正文、凭据或节点隐私数据。

```json
{
  "uptime_seconds": 3600,
  "summary": {
    "fresh_cache_hits": 1200,
    "stale_cache_hits": 3,
    "cache_misses": 1,
    "builds": 401,
    "build_failures": 0,
    "build_total_ms": 832.4,
    "build_max_ms": 12.7,
    "last_bytes": 39717
  },
  "sqlite": {
    "busy_retries": 0,
    "outbox_pending": 0,
    "outbox_leased": 0,
    "outbox_failed": 0,
    "latency_rollup_rows_approx": 120000,
    "state_rollup_rows_approx": 85000,
    "raw_probe_rounds_approx": 160000,
    "raw_state_rows_approx": 260000,
    "rollup_enabled_after": "2026-08-01T03:00:00Z",
    "rollup_ready": false
  }
}
```

`*_approx` 使用主键/rowid 范围做常数级估算，历史清理产生间隙时可能高于实际行数；这样不会为了展示观测数据扫描数百万行活动数据库。

### GET /api/admin/v1/settings

读取后台可编辑的站点配置。响应包装在 `settings` 字段下，只返回公开可展示字段，不返回任何凭据或 hash。

```json
{
  "settings": {
    "site_title": "Zeno",
    "logo_url": "/assets/logo/id.png",
    "theme": "system",
    "agent_controller_url": "",
    "background_url": "",
    "desktop_background_url": "",
    "mobile_background_url": "",
    "appearance_preset": "default",
    "card_opacity": 0.7,
    "card_blur": 0,
    "card_radius": 20,
    "border_strength": 0.3,
    "shadow_strength": 0.2,
    "background_overlay": 0,
    "theme_color": "#2563eb",
    "custom_code": "",
    "revision": 0,
    "updated_at": "2026-07-04T12:00:00Z"
  }
}
```

### PATCH /api/admin/v1/settings

部分更新站点配置。`expected_revision` 必须使用最近一次 GET/PATCH 响应中的 `revision`；其余设置字段均可省略。Controller 会在同一 SQLite 事务内比较并递增单调 revision，再 trim 文本并持久化设置，避免两个后台标签页把较新的设置静默覆盖。

请求：

```json
{
  "expected_revision": 0,
  "site_title": "水饺监控",
  "logo_url": "/assets/logo/custom.png",
  "theme": "dark",
  "agent_controller_url": "https://zeno.example.com",
  "background_url": "https://example.com/desktop-bg.webp",
  "desktop_background_url": "https://example.com/desktop-bg.webp",
  "mobile_background_url": "https://example.com/mobile-bg.webp",
  "appearance_preset": "gaussian_blur",
  "card_opacity": 0.5,
  "card_blur": 15,
  "card_radius": 20,
  "border_strength": 0.3,
  "shadow_strength": 0.3,
  "background_overlay": 0.05,
  "theme_color": "#2563eb",
  "custom_code": "<style>.home-top-card { border-color: #2563eb; }</style>"
}
```

约束：

- `expected_revision` 必填且必须是非负整数；与当前 revision 不一致时返回 `409 settings changed`，数据库不会写入任何字段。客户端应重新读取设置、保留本地草稿，并由用户决定是否载入最新版后重试。revision 不使用秒级时间戳，因此同一秒内的连续保存也能可靠区分。
- `site_title` 不能为空，最长 64 个字符。
- `theme` 只能是 `system`、`dark` 或 `light`。
- `agent_controller_url` 可为空；非空时不得包含用户名密码、query 或 fragment。远程地址默认使用 `https://`；真实 loopback host 可使用 `http://`，没有反向代理时也允许 `http://<直接 IP>:<显式端口>`。后台复制命令前会再次确认风险，生成的命令显式传入 `ZENO_ALLOW_INSECURE_HTTP=1`，安装器再把 runtime opt-in 持久化；enrollment/runtime bearer token 仍会明文传输。主机名 HTTP 和没有显式端口的远程 HTTP 会被拒绝。为空时可回退到当前后台地址，但仍执行同一规则。
- `logo_url` 必须是站内绝对路径（如 `/assets/logo/id.png`）或 `https://` URL；当前首页/后台头部头像与 Logo 都使用这一字段。
- `background_url` 是旧兼容字段，当前等价于电脑端背景图；`background_url`、`desktop_background_url`、`mobile_background_url` 均可为空，非空时必须是站内绝对路径或 `https://` URL。手机端背景留空时前端跟随电脑端背景。
- `appearance_preset` 只能是 `default` 或 `gaussian_blur`。预设会在前端作为默认参数组合，具体样式仍由下面的数值字段控制。
- `card_opacity` 范围 `0.2`–`1`；`card_blur` 范围 `0`–`40`；`card_radius` 范围 `8`–`36`；`border_strength` / `shadow_strength` 范围 `0`–`1`；`background_overlay` 范围 `0`–`0.8`；`theme_color` 必须是 `#RRGGBB`。
- `custom_code` 可为空，当前作为 CSS-only 外观扩展注入公开页面：前端只提取 `<style>` 内容或纯 CSS 文本，不执行 `<script>`，也不挂载 HTML 事件处理器；最长 60000 字符。这里是公开展示样式，不要写入 token、secret 或 credential。
- 图片只通过 URL / 站内静态路径引用，不把外观图片写入数据库。
- 后台保存前会先做同口径的客户端校验，减少提交后才被 API 拒绝的情况。
- 响应仍只返回公开展示字段，不返回 Admin token、Agent token、token hash、secret、credential 或任何凭据值。

### GET /api/admin/v1/nodes

节点管理列表，返回 enabled + disabled 节点、状态、地区、到期日、续费金额、币种、账单周期、显示顺序、公网 IPv4/IPv6、流量计费口径、月流量重置日、配额、last seen、host info 和 agent version。列表按 `display_order ASC, id ASC` 排序。

响应字段重点：

```json
{
  "nodes": [
    {
      "id": "example-node-a",
      "display_name": "Example Node A",
      "status": "online",
      "country_code": "HK",
      "region": "Hong Kong",
      "disabled": false,
      "billing_mode": "both",
      "monthly_reset_day": 1,
      "expiry_date": "2026-08-01",
      "billing_cycle": "月付",
      "renewal_amount": 20,
      "renewal_currency": "USD",
      "display_order": 10,
      "public_ipv4": "198.51.100.8",
      "public_ipv6": "2001:db8::8",
      "monthly_quota_bytes": 1099511627776,
      "last_seen_at": "2026-07-03T00:00:00Z",
      "created_at": "2026-07-02T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z",
      "agent_version": "a0cd835"
    }
  ]
}
```

### POST /api/admin/v1/nodes

新增服务器。Zeno 的服务器接入流程是先在后台添加服务器并编辑名称/地区/配额等管理字段，然后点击“复制安装命令”。

请求：

```json
{
  "display_name": "New Server",
  "country_code": "HK",
  "region": "Hong Kong",
  "expiry_date": "2026-08-01",
  "billing_cycle": "月付",
  "renewal_amount": 20,
  "renewal_currency": "USD",
  "billing_mode": "both",
  "monthly_reset_day": 1,
  "display_order": 10,
  "public_ipv4": "198.51.100.8",
  "public_ipv6": "2001:db8::8",
  "monthly_quota_bytes": 1099511627776
}
```

响应返回新节点 DTO，但不会返回 Agent token 原文或 token hash。新节点默认 `status=no_data`，并为现有探针目标建立默认未启用的服务器关联；管理员选择延迟监控后才会向该节点下发。`renewal_amount` 可为空或为大于 0 的金额；`renewal_currency` 支持 `CNY`、`USD`、`HKD`、`EUR`、`GBP`、`JPY`、`SGD`、`AUD`、`CAD`、`KRW`，默认 `CNY`。`billing_mode` 可选 `both`、`in`、`out`、`max`，默认 `both`；`monthly_reset_day` 范围 1–31，默认 1。`expiry_date` 为空时清空到期日；非空时必须是 `YYYY-MM-DD`。

每次复制安装命令都会生成一个有效期 10 分钟、只能兑换一次的 enrollment token，并立即撤销该节点先前尚未使用的 enrollment；命令不会包含或复用 runtime token。生成命令本身不会中断已在线 Agent：当前 runtime token 继续有效。安装器兑换 enrollment 后会生成随机 runtime token；新 Agent 首次用该 token 成功鉴权时，Controller 才原子切换 runtime token，旧 runtime token 随即失效。后台 UI 提供 Linux / macOS / Windows 三种命令和复制按钮。命令中的 Controller 地址优先使用站点设置里的 `agent_controller_url`；未设置时才使用当前后台请求地址。未显式配置 Agent 版本时，安装脚本解析 Zeno-Agent 最新稳定 release。

### PATCH /api/admin/v1/nodes/reorder

一次提交完整服务器顺序。后台直接在服务器列表内拖动，或聚焦拖动手柄后使用上下方向键调整；操作完成即调用本接口，不再经过额外的保存/取消步骤。`node_ids` 必须无重复地覆盖当前全部可见服务器；后端先校验整组 ID，再在单个 SQLite 事务中按 10、20、30… 写入 `display_order`。任一校验或写入失败都会整体回滚，不会留下部分排序；成功返回 `204 No Content`。

```json
{
  "node_ids": ["node-c", "node-a", "node-b"]
}
```

### PATCH /api/admin/v1/nodes/{node_id}

更新节点可编辑管理字段。不会返回 token 原文或 token hash。

请求：

```json
{
  "display_name": "Example Node A",
  "country_code": "HK",
  "region": "Hong Kong",
  "expiry_date": "2026-08-01",
  "billing_cycle": "月付",
  "renewal_amount": 20,
  "renewal_currency": "USD",
  "billing_mode": "max",
  "monthly_reset_day": 15,
  "display_order": 10,
  "public_ipv4": "198.51.100.8",
  "public_ipv6": "2001:db8::8",
  "monthly_quota_bytes": 1099511627776,
  "home_probe_target_id": "cloudflare",
  "probe_target_ids": ["cloudflare", "google"],
  "disabled": false
}
```

字段均可部分提交；`monthly_quota_bytes: null` 表示清空月配额，`renewal_amount: null` 表示清空续费金额；`expiry_date` / `billing_cycle` / `public_ipv4` / `public_ipv6` 提交空字符串表示清空。币种范围与创建接口一致。`billing_mode` 可选 `both`（入站+出站）、`in`（只算入站）、`out`（只算出站）、`max`（入/出取较大）；`monthly_reset_day` 范围 1–31。

编辑服务器时可同时提交 `probe_target_ids`，后端会在同一事务内替换该服务器的延迟监控关联并更新 `home_probe_target_id`，避免前端为每个目标分别发送 PATCH。首页目标非空时必须包含在 `probe_target_ids` 中；空数组表示取消全部关联。

响应：

```json
{
  "node": {
    "id": "example-node-a",
    "display_name": "Example Node A",
    "status": "online",
    "country_code": "HK",
    "region": "Hong Kong",
      "disabled": false,
      "billing_mode": "max",
      "monthly_reset_day": 15,
      "expiry_date": "2026-08-01",
    "billing_cycle": "月付",
    "display_order": 10,
    "public_ipv4": "198.51.100.8",
    "public_ipv6": "2001:db8::8",
    "monthly_quota_bytes": 1099511627776,
    "created_at": "2026-07-02T00:00:00Z",
    "updated_at": "2026-07-03T00:00:00Z"
  }
}
```

### GET /api/admin/v1/probe-targets

探针目标管理列表，返回所有有效目标、显示顺序及分配到哪些节点。目标创建后默认有效，不提供全局启用/停用开关；是否由某台服务器执行探测，只由 `assignments[].enabled` 控制。列表按 `display_order ASC, id ASC` 排序；后台直接在延迟监控列表内拖动，或聚焦拖动手柄后使用上下方向键调整，操作完成即通过批量接口写入完整顺序。不会返回 Agent token、token hash 或 secret 字段。

响应：

```json
{
  "targets": [
    {
      "id": "example-node-a-local",
      "name": "Example Node A",
      "type": "tcping",
      "address": "127.0.0.1",
      "port": 18980,
      "count": 3,
      "timeout_ms": 600,
      "interval_sec": 30,
      "display_order": 10,
      "assignments": [
        {
          "node_id": "example-node-a",
          "node_display_name": "Example Node A",
          "enabled": true
        }
      ]
    },
    {
      "id": "zeno-health",
      "name": "Zeno Health",
      "type": "http_get",
      "address": "https://example.com/health",
      "port": null,
      "count": 2,
      "timeout_ms": 600,
      "interval_sec": 30,
      "display_order": 20,
      "assignments": []
    }
  ]
}
```

目标类型规则：`tcping` 必须提交有效 `port`；`ping`/`icmp` 会归一成 `ping` 且 `port` 为 `null`；`http`/`https`/`http_get` 会归一成 `http_get`，`port` 为 `null`。HTTP URL 仅允许 loopback 或“直接 IP + 显式端口”，HTTPS 可使用普通主机名；两者均拒绝 userinfo。资源上限同 Agent 下发口径：单节点最多 32 个启用目标，`count` 1–32，`timeout_ms` 100–5000，`interval_sec` 5–3600，且单目标/单节点 round 预算不能超限；超限的新增或编辑请求会返回 `400`。

### POST /api/admin/v1/probe-targets

新增探针目标。新目标创建后立即作为有效目标存在；未提交 `assignments` 时默认不关联任何服务器。未提交 `display_order`（或提交 0）时，Controller 在同一事务中把目标追加到当前最大顺序之后，避免随机生成的目标 ID 影响默认顺序；响应仍不包含 Agent 凭据。

HTTP GET 示例：

```json
{
  "name": "Zeno Health",
  "type": "http_get",
  "address": "https://example.com/health",
  "port": null,
  "count": 2,
  "timeout_ms": 600,
  "interval_sec": 30,
  "display_order": 20
}
```

### PATCH /api/admin/v1/probe-targets/reorder

一次提交完整延迟监控顺序。`target_ids` 必须无重复地覆盖当前全部可见目标；后端先校验整组 ID，再在单个 SQLite 事务中按 10、20、30… 写入 `display_order`，同时递增探针配置版本，使 Agent 重新读取顺序。任一校验或写入失败都会整体回滚，不会留下部分排序；成功返回 `204 No Content`。

```json
{
  "target_ids": ["target-c", "target-a", "target-b"]
}
```

### PATCH /api/admin/v1/probe-targets/{target_id}

更新探针目标配置、显示顺序或节点分配。`display_order` 必须是非负整数；`assignments` 省略表示不改变分配，传入时按 `node_id` 更新启用状态。

切换到 `http_get` 时必须同时保证最终 `address` 是完整 URL；Controller 会清空旧 TCP `port` 并以 `null` 返回。切回 `tcping` 时必须提交有效 `port`。

### DELETE /api/admin/v1/probe-targets/{target_id}

删除探针目标。成功返回 `204 No Content`；不存在返回 `404`。删除会同时清理该目标的节点分配和历史 probe round/sample 记录。响应不会返回 Agent token、token hash、secret 或任何凭据字段。

### GET /api/admin/v1/alert-rules

通知类型规则库存。Controller 启动或迁移时会 seed 一组默认规则。后台通知页只展示已启用/已添加的规则，未启用的预置规则通过“添加通知类型”弹窗选择。规则默认作用于全部服务器；`scope_node_ids` 非空时只作用于这些服务器。响应只包含规则配置、作用范围和通知事件标签，不返回 admin token、Agent token、token hash、通知渠道凭据、secret 或 credential 原文。

默认规则覆盖：CPU、内存、磁盘、离线通知和续费提醒。资源规则映射到 `probe_unhealthy` / 异常；离线规则映射到 `node_offline` / 离线；续费规则映射到 `renewal_due` / 续费，并通过 `renewal_days` 返回一个或多个提前提醒时间。

Controller 会在 Agent 上报时实际使用这些规则：

- `/api/agent/v1/state` 会按启用的资源规则评估 `cpu_percent`、内存使用率、磁盘使用率。资源规则的 `duration_sec` 表示统计窗口，默认按 5 分钟平均值超过阈值时把节点公共状态置为 `warning` 并进入 `probe_unhealthy` 通知链路。
- `/api/agent/v1/probe-results` 只写入探针历史，不再通过延迟或丢包阈值改变节点公共状态。
- 资源规则命中状态会记录在 Controller 内部的 `alert_rule_states` 表，用来避免某一类健康上报误清另一类仍活跃的异常；`alert_rule_states` 只作为 Controller 内部命中状态存储。
- 如果规则配置了 `scope_node_ids`，Agent 上报、规则命中和通知发送都会只对这些服务器生效；空数组表示全部服务器。离线规则的 `duration_sec` 同时作为公共在线/离线状态与离线通知的心跳超时时间，默认 60 秒；presence WebSocket 和服务探测结果不覆盖页面在线状态；Controller 启动后先留出一个完整心跳窗口，再每 5 秒补扫一次过期 `last_seen_at`，把漏掉的离线状态落库并进入同一条 `node_offline` 通知链路。
- 续费规则的 `renewal_days` 是去重后的提前提醒天数数组，支持 `1`、`3`、`7`、`15` 和 `30`；每个选中时间点只触发一次，`30` 按自然月计算。兼容字段 `threshold` 保存数组中的最大值，旧客户端仍可只提交单个 `threshold`。
- 通知发送同时要求：状态转换存在、对应通知类型启用、至少一条映射到该事件类型且对该服务器生效的规则启用、且存在启用并配置好的通知渠道。

```json
{
  "rules": [
    {
      "id": "cpu_high",
      "name": "CPU 使用率",
      "category": "resource",
      "metric": "cpu_percent",
      "comparator": ">=",
      "threshold": 90,
      "threshold_unit": "%",
      "duration_sec": 300,
      "enabled": true,
      "notification_event_type": "probe_unhealthy",
      "notification_label": "异常",
      "description": "",
      "scope_node_ids": [],
      "created_at": "2026-07-03T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z"
    },
    {
      "id": "node_offline",
      "name": "离线通知",
      "category": "liveness",
      "metric": "heartbeat_age_sec",
      "comparator": ">=",
      "threshold": 60,
      "threshold_unit": "s",
      "duration_sec": 60,
      "enabled": true,
      "notification_event_type": "node_offline",
      "notification_label": "离线",
      "description": "",
      "scope_node_ids": ["example-node-a"],
      "created_at": "2026-07-03T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z"
    }
  ]
}
```

### PATCH /api/admin/v1/alert-rules/{rule_id}

部分更新通知类型规则的安全可调字段。当前允许调整启用状态、阈值、续费提醒时间、统计窗口/确认时间和作用服务器范围；启用状态在 Admin 中表现为添加 / 移除通知类型。规则 id、名称、指标、比较符、通知事件类型等结构性字段由 seed/代码控制。`scope_node_ids` 省略表示保持原范围不变，空数组表示作用于全部服务器，非空数组表示只作用于这些服务器；数组里的 node id 必须存在且不能重复。`renewal_days` 只适用于续费规则，不能为空、不能重复，且不能和兼容字段 `threshold` 同时提交。前端保存通知类型时会等待该请求成功后才关闭弹窗；如后端后续提供原子化“规则 + 通知事件类型”接口，应优先改用原子接口，当前兼容路径失败时会留在弹窗并显示短错误。

请求：

```json
{
  "enabled": true,
  "threshold": 85,
  "duration_sec": 300,
  "scope_node_ids": ["example-node-a", "backup"]
}
```

字段均可部分提交；`threshold` 和 `duration_sec` 必须是非负数。成功响应返回更新后的单条规则：

续费提醒多选请求示例：

```json
{
  "enabled": true,
  "renewal_days": [1, 3, 7],
  "scope_node_ids": ["example-node-a", "backup"]
}
```

```json
{
  "rule": {
    "id": "cpu_high",
    "name": "CPU 使用率",
    "category": "resource",
    "metric": "cpu_percent",
    "comparator": ">=",
    "threshold": 85,
    "threshold_unit": "%",
    "duration_sec": 300,
    "enabled": true,
    "notification_event_type": "probe_unhealthy",
    "notification_label": "异常",
    "description": "",
    "scope_node_ids": ["example-node-a", "backup"],
    "created_at": "2026-07-03T00:00:00Z",
    "updated_at": "2026-07-03T00:05:00Z"
  }
}
```

关系说明：`alert_rules` 决定“什么时候形成某类状态事件”以及是否发送；`notification_channels` 决定发送到哪些启用渠道。`notification_types` 仅保留为旧 API 兼容层，不再作为发送前置条件。

### GET /api/admin/v1/notification-channels

Telegram 通知渠道管理列表。Zeno 当前只支持 Telegram 一个通知渠道类型；API 不暴露 `type` / `channel_type`，也不返回已保存的 Bot Token。后台只能通过 `credential_set` 判断是否已配置凭据；编辑弹窗的 Token 输入框留空表示保留原值。

```json
{
  "channels": [
    {
      "id": "telegram-home",
      "name": "Telegram Home",
      "destination": "7579942307",
      "credential_set": true,
      "enabled": true,
      "created_at": "2026-07-03T00:00:00Z",
      "updated_at": "2026-07-03T00:00:00Z"
    }
  ]
}
```

### POST /api/admin/v1/notification-channels

新增 Telegram 通知渠道。`destination` 是 Telegram chat id；`credential` 是 Telegram Bot Token，只写入保存用于后续 `sendMessage`，任何列表/创建/更新响应都不会回显原文，只返回 `credential_set` 表示是否已配置。请求不接受 `type` 字段。

```json
{
  "name": "Telegram Home",
  "destination": "7579942307",
  "credential": "***",
  "enabled": true
}
```

### PATCH /api/admin/v1/notification-channels/{channel_id}

部分更新 Telegram 通知渠道。省略 `credential`（后台编辑框留空时也会省略）会保留旧 Bot Token；传入新 `credential` 时覆盖旧 Bot Token。响应仍只返回 `credential_set`，不返回 `credential` 原文。请求不接受 `type` 字段。

### DELETE /api/admin/v1/notification-channels/{channel_id}

删除通知渠道。成功返回 `204 No Content`；不存在返回 `404`。

### POST /api/admin/v1/notification-channels/{channel_id}/test

显式测试某个已启用的 Telegram 通知渠道。这个接口只在后台管理员点击“测试发送”时调用，同步发送一条合成的 `test_notification` / `测试发送` 事件。

响应只返回本次测试发送结果 DTO，不返回渠道 Bot Token、token 原文、secret、credential、Authorization header 或任何 hash。

```json
{
  "delivery": {
    "event_type": "test_notification",
    "label": "测试发送",
    "node_id": "admin-test",
    "node_name": "Zeno",
    "previous_status": "test",
    "status": "test",
    "channel_id": "telegram-home",
    "channel_name": "Telegram Home",
    "success": true,
    "created_at": "2026-07-03T00:10:00Z"
  }
}
```

### PATCH /api/admin/v1/notification-types/{event_type}

兼容旧后台的一对一通知类型开关，仅支持 `node_offline` 和 `renewal_due`，并同步更新同名 alert rule。新的后台应使用 `/api/admin/v1/alert-rules/{rule_id}` 精确启用/关闭单条规则。`probe_unhealthy` 由 CPU、内存、磁盘等多条独立规则共享，调用此兼容接口会返回 `410 Gone`，避免成功但不生效或误批量修改。

```json
{
  "enabled": true
}
```

## 通知发送

状态类发送逻辑挂在 Agent 状态变化、资源状态上报和探测结果上报之后：

- 非 `offline` → `offline`：触发 `node_offline`。
- 非 `warning` → `warning`：触发 `probe_unhealthy`。
- `/api/agent/v1/state` 会依据启用的 CPU/内存/磁盘通知类型规则触发或保持 `warning`。
- `/api/agent/v1/probe-results` 只写入探针历史，不触发异常通知。
- 状态未变化时不重复发送。
- `renewal_due` 续费提醒由 Controller 独立低频定时任务扫描，不挂在 heartbeat/host/state 高频请求上。扫描会在同一 SQLite 事务中完成按日 claim 和 outbox delivery 创建；Controller 崩溃后由 outbox 继续发送，且并发扫描不会重复创建同一天/同到期日的提醒。

发送前要求：对应 `alert_rules.enabled = 1`，且至少一个 `notification_channels.enabled = 1` 并已配置 Telegram chat id 和 Bot Token。显式 `POST /notification-channels/{channel_id}/test` 是管理员手动测试，但仍要求渠道处于启用状态并已配置凭据。

渠道语义：Telegram-only。`destination` 是 chat id，`credential` 是 Bot Token；Controller 调用 Telegram Bot API 的 `sendMessage`。

通知发送失败不会阻塞 Agent 心跳/状态写入；凭据不会出现在 JSON 响应或通知 payload body 中。

Admin API 返回中必须隐藏 token 原文、token hash、通知渠道凭据原文和 secret 字段。
