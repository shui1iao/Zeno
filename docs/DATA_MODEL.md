# Data Model / SQLite 数据模型

Zeno 使用 SQLite。schema 不兼容任何旧系统。

## nodes

服务器 / Agent 节点。

```sql
CREATE TABLE nodes (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  token_hash TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'no_data',
  country_code TEXT,
  region TEXT,
  expiry_date TEXT,
  billing_cycle TEXT,
  renewal_amount REAL,
  renewal_currency TEXT NOT NULL DEFAULT 'CNY',
  display_order INTEGER NOT NULL DEFAULT 0,
  public_ipv4 TEXT,
  public_ipv6 TEXT,
  billing_mode TEXT NOT NULL DEFAULT 'both',
  monthly_quota_bytes INTEGER,
  monthly_reset_day INTEGER NOT NULL DEFAULT 1,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_seen_at INTEGER
);
```

说明：

- `country_code` 用于国旗展示。
- `expiry_date` / `billing_cycle` 用于后台和首页展示到期/账单信息。
- `renewal_amount` / `renewal_currency` 保存每台服务器的原币种续费金额；公开 Summary 额外给出按当天 Google Finance 汇率折算后的人民币月均消费。
- `display_order` 控制首页卡片和后台列表排序。
- `public_ipv4` / `public_ipv6` 可由后台编辑，也会由新 Agent best-effort 自动识别后上报；识别失败不会清空已有值。
- `billing_mode` 控制月流量口径：`both`、`in`、`out`、`max`。
- `monthly_reset_day` 控制账单周期从每月第几天开始，范围 1–31。
- `token_hash` 只存 hash，不通过 API 返回。

## exchange_rates

持久化最近一次从 Google Finance 成功获取的人民币汇率，避免外部汇率页面暂时不可用时让金额统计消失。

```sql
CREATE TABLE exchange_rates (
  currency TEXT PRIMARY KEY,
  cny_rate REAL NOT NULL,
  source_date TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL
);
```

Controller 启动时立即刷新一次，之后每 24 小时刷新；只有完整响应通过校验后才原子替换缓存。

## host_info

相对低频上报的主机信息。

```sql
CREATE TABLE host_info (
  node_id TEXT PRIMARY KEY REFERENCES nodes(id),
  hostname TEXT,
  os_name TEXT,
  os_version TEXT,
  kernel TEXT,
  arch TEXT,
  virtualization TEXT,
  cpu_model TEXT,
  cpu_cores INTEGER,
  memory_total_bytes INTEGER,
  disk_total_bytes INTEGER,
  boot_time INTEGER,
  agent_version TEXT,
  updated_at INTEGER NOT NULL
);
```

## state_samples

实时资源状态样本。

```sql
CREATE TABLE state_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id TEXT NOT NULL REFERENCES nodes(id),
  ts INTEGER NOT NULL,
  cpu_percent REAL,
  load1 REAL,
  load5 REAL,
  load15 REAL,
  memory_used_bytes INTEGER,
  memory_total_bytes INTEGER,
  swap_used_bytes INTEGER,
  swap_total_bytes INTEGER,
  disk_used_bytes INTEGER,
  disk_total_bytes INTEGER,
  net_in_total_bytes INTEGER,
  net_out_total_bytes INTEGER,
  net_in_speed_bps REAL,
  net_out_speed_bps REAL,
  process_count INTEGER,
  tcp_connection_count INTEGER,
  uptime_seconds INTEGER
);

CREATE INDEX idx_state_samples_node_ts ON state_samples(node_id, ts);
```

超过 26 小时的资源样本会事务性折叠到 `state_history_rollups`：每个节点每 30 秒一行，对每项指标分别保存 `sum + count`，因此旧 Agent 缺失字段仍保持 `null` 语义。7 天和 30 天查询把 raw 与 rollup 统一再次分桶，接口精度和时间范围不变。

## traffic_monthly

月流量累计。Controller 根据 state 的累计 counter delta 更新；`month` 是该节点当前计费周期开始月份（例如 `monthly_reset_day=15` 且当前周期为 2026-06-15 至 2026-07-14 时，`month=2026-06`）。

```sql
CREATE TABLE traffic_monthly (
  node_id TEXT NOT NULL REFERENCES nodes(id),
  month TEXT NOT NULL,
  billing_epoch INTEGER NOT NULL DEFAULT 0,
  reset_day INTEGER NOT NULL DEFAULT 1,
  billing_mode TEXT NOT NULL DEFAULT 'both',
  in_bytes INTEGER NOT NULL DEFAULT 0,
  out_bytes INTEGER NOT NULL DEFAULT 0,
  billable_bytes INTEGER NOT NULL DEFAULT 0,
  last_in_total_bytes INTEGER,
  last_out_total_bytes INTEGER,
  last_sample_ts INTEGER,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (node_id, month, billing_epoch)
);
```

## traffic_lifetime

首页永久累计流量。首次有效 state 样本把当时的网卡 counter 作为起点，之后按 counter delta 累计；counter 因服务器、Agent 或网卡重启而降低时，把重置后的较小 counter 视为重置后已经产生的流量并计入永久累计，同时将其设为下一次采样的基线。因此 `in_bytes` / `out_bytes` 不会因重启倒退或漏掉重置后的首次上报，也不受月重置日、计费口径或 billing epoch 变化影响。

旧数据库升级时，Controller 仅使用每台节点最新的有效 raw counter 进行一次性回填，并以该样本作为后续基线，不尝试从可能已裁剪的历史记录中重建永久累计值。

```sql
CREATE TABLE traffic_lifetime (
  node_id TEXT PRIMARY KEY REFERENCES nodes(id) ON DELETE CASCADE,
  in_bytes INTEGER NOT NULL DEFAULT 0,
  out_bytes INTEGER NOT NULL DEFAULT 0,
  last_in_total_bytes INTEGER,
  last_out_total_bytes INTEGER,
  last_sample_ts INTEGER,
  updated_at INTEGER NOT NULL
);
```

## probe_targets

探测目标。

```sql
CREATE TABLE probe_targets (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  address TEXT NOT NULL,
  port INTEGER,
  count INTEGER NOT NULL,
  timeout_ms INTEGER NOT NULL,
  interval_sec INTEGER NOT NULL,
  display_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

`type` 当前支持：

- `tcping`：TCP connect，必须带 `port`。
- `ping`：ICMP ping，不带 `port`。
- `http_get`：HTTP/HTTPS GET，不带 `port`。

`display_order` 控制后台延迟监控列表、Agent 目标下发顺序、服务详情入口顺序和同一时间点的 Public latency series 展示顺序。历史数据库中仍为 0 的目标会在启动迁移时追加到已有正数顺序之后，并按 `created_at`、`id` 归一化为稳定的 10、20、30…；新目标未提交顺序时在创建事务中追加到当前最大顺序之后。探测目标创建后即为有效目标，不提供全局启用/停用状态；是否由某台服务器执行只由 `node_probe_targets.enabled` 控制。

## node_probe_targets

哪些节点执行哪些探测目标。

```sql
CREATE TABLE node_probe_targets (
  node_id TEXT NOT NULL REFERENCES nodes(id),
  target_id TEXT NOT NULL REFERENCES probe_targets(id),
  enabled INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (node_id, target_id)
);
```

## probe_rounds / probe_samples

每轮探测摘要和 raw samples。
Public 服务详情页按 `target_id` 查询所有节点的 `probe_rounds`，把同一监控服务拆成多条节点曲线。

```sql
CREATE TABLE probe_rounds (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id TEXT NOT NULL REFERENCES nodes(id),
  target_id TEXT NOT NULL REFERENCES probe_targets(id),
  ts INTEGER NOT NULL,
  type TEXT NOT NULL,
  sent INTEGER NOT NULL,
  received INTEGER NOT NULL,
  loss_percent REAL NOT NULL,
  min_ms REAL,
  avg_ms REAL,
  median_ms REAL,
  max_ms REAL,
  stddev_ms REAL,
  error TEXT
);

CREATE INDEX idx_probe_rounds_node_target_ts ON probe_rounds(node_id, target_id, ts);

CREATE TABLE probe_samples (
  round_id INTEGER NOT NULL REFERENCES probe_rounds(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  success INTEGER NOT NULL,
  latency_ms REAL,
  error TEXT,
  PRIMARY KEY (round_id, seq)
);
```

超过 26 小时的探测轮次会事务性折叠到 `latency_history_rollups`：每个节点、目标每分钟一行，分别保存 median、average、loss 的 `sum + count`。同一事务随后删除对应 `probe_rounds`，关联的 `probe_samples` 通过外键级联删除；进程中断不会留下重复汇总或先删后丢数据。

Controller 首次创建 rollup schema 后保留 24 小时回滚宽限期；期间继续保留完整 30 天 raw，上一版本仍能读取全部支持范围。宽限期结束后，每小时以有界批次逐步维护 raw 与 rollup，只保留最近 30 天的汇总历史。30 天边界按各自 bucket 向下对齐后保留；配置、当前状态、月流量和通知事件标记不属于高频历史，不随该任务删除。这样 1 天视图继续使用原始精度，7 天/30 天视图保持原有 7 分钟、30 分钟以及资源 30 分钟、2 小时网格，同时显著减少长期 raw sample 与索引写放大。

## notification_channels / notification_types

通知当前是 Telegram-only 产品路径；SQLite schema 不保留多渠道 `type` / `channel_type` 兼容列。

```sql
CREATE TABLE notification_channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  destination TEXT NOT NULL,
  credential TEXT NOT NULL,
  delivery_version INTEGER NOT NULL DEFAULT 1,
  destination_fingerprint TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE notification_types (
  event_type TEXT PRIMARY KEY,
  enabled INTEGER NOT NULL DEFAULT 0,
  updated_at INTEGER NOT NULL
);

CREATE TABLE notification_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id TEXT NOT NULL DEFAULT '',
  event_type TEXT NOT NULL,
  label TEXT NOT NULL DEFAULT '',
  node_id TEXT NOT NULL DEFAULT '',
  node_name TEXT NOT NULL DEFAULT '',
  node_ip TEXT NOT NULL DEFAULT '',
  previous_status TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT '',
  event_ts TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  channel_id TEXT NOT NULL,
  channel_name TEXT NOT NULL DEFAULT '',
  channel_version INTEGER NOT NULL DEFAULT 1,
  destination_fingerprint TEXT NOT NULL DEFAULT '',
  state TEXT NOT NULL DEFAULT 'pending',
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at INTEGER NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  lease_until INTEGER NOT NULL DEFAULT 0,
  claim_token TEXT NOT NULL DEFAULT '',
  causal_predecessor_event_id TEXT NOT NULL DEFAULT '',
  superseded_by_event_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  delivered_at INTEGER
);

```

```sql
CREATE TABLE notification_states (
  channel_id TEXT NOT NULL,
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,                 -- node_offline / probe_unhealthy / renewal_due
  notified TEXT NOT NULL DEFAULT '',  -- 已告诉用户的状态：online/offline、ok/warning、<dueDate>#<threshold>
  notified_detail TEXT NOT NULL DEFAULT '', -- 资源告警时的规则名，例如 "CPU、内存"
  incident_from INTEGER NOT NULL DEFAULT 0, -- 事件起点（unix 秒），首次观察到告警状态时写入
  pending_target TEXT NOT NULL DEFAULT '',
  pending_since INTEGER NOT NULL DEFAULT 0, -- 首次观察到 pending_target 的时间
  pending_attempts INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  channel_version INTEGER NOT NULL DEFAULT 1,
  destination_fingerprint TEXT NOT NULL DEFAULT '',
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (channel_id, node_id, kind)
);

CREATE TABLE notification_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts INTEGER NOT NULL,
  channel_id TEXT NOT NULL DEFAULT '',
  node_id TEXT NOT NULL DEFAULT '',
  node_name TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL DEFAULT '',
  from_state TEXT NOT NULL DEFAULT '',
  to_state TEXT NOT NULL DEFAULT '',
  outcome TEXT NOT NULL,              -- sent / failed / dropped / baseline / rebaseline
  attempt INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',     -- 脱敏错误
  message TEXT NOT NULL DEFAULT ''
);
```

`credential` 不通过 Admin API 响应返回。`notification_states` 只保存“已告诉用户的状态”，真实状态
每轮从 `nodes.status`、`alert_rule_states` 和续费规则读取，不维护影子状态；行内记录的
`channel_version` + `destination_fingerprint` 与渠道当前绑定不一致时静默重新基线。节点删除时级联
删除，渠道删除时一并删除对应行。`notification_log` 记录 sent/failed/dropped/baseline/rebaseline，
只保存脱敏错误，纳入通知历史保留期清理。对账与发送规则见 `docs/API.md` 的“通知发送”。

`notification_types` 仅保留旧 API 兼容，发送开关以 `alert_rules.enabled` 为准；兼容 PATCH 的两表写入
在同一事务内完成。

`notification_deliveries` 和 `notification_event_marks` 是旧 outbox 模型的表，当前版本保留表结构和
数据（便于回滚），但不再写入、清理或读取（升级时的一次性种子迁移除外）。种子迁移
`20261008_notification_reconcile_seed_v1` 对每个启用渠道、每个节点的 `node_offline`/`probe_unhealthy`，
取与渠道当前绑定一致、`state = 'delivered'` 的最新一行作为 `notified`；续费取今天之前的最新提醒点，
今天已有 `notification_event_marks` 标记的也算已通知。没有已送达记录的组合由首轮对账静默基线。

## alert_rules / alert_rule_node_scopes / alert_rule_renewal_days / alert_rule_states

通知类型规则和内部命中状态。

```sql
CREATE TABLE alert_rules (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  category TEXT NOT NULL,
  metric TEXT NOT NULL,
  comparator TEXT NOT NULL,
  threshold REAL NOT NULL,
  threshold_unit TEXT NOT NULL,
  duration_sec INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  notification_event_type TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE INDEX idx_alert_rules_sort_order ON alert_rules(sort_order ASC, id ASC);

CREATE TABLE alert_rule_node_scopes (
  rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  node_id TEXT NOT NULL REFERENCES nodes(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (rule_id, node_id)
);

CREATE INDEX idx_alert_rule_node_scopes_node ON alert_rule_node_scopes(node_id, rule_id);

CREATE TABLE alert_rule_renewal_days (
  rule_id TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
  days INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (rule_id, days)
);

CREATE TABLE alert_rule_states (
  node_id TEXT NOT NULL REFERENCES nodes(id),
  rule_id TEXT NOT NULL REFERENCES alert_rules(id),
  active INTEGER NOT NULL DEFAULT 0,
  first_seen_at INTEGER,
  last_seen_at INTEGER,
  last_value REAL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (node_id, rule_id)
);

CREATE INDEX idx_alert_rule_states_node_active ON alert_rule_states(node_id, active);
```

`alert_rule_node_scopes` 没有记录时表示规则作用于全部服务器；有记录时只作用于指定服务器。
`alert_rule_renewal_days` 保存续费规则的多个提前提醒时间；每个 `(rule_id, days)` 唯一。`alert_rules.threshold` 继续保存最大提前天数，供旧客户端兼容读取和单值更新。

## settings

全局设置，包含站点标题、Logo、主题、Agent 接入 URL、桌面/手机背景图配置和后台密码 hash。

```sql
CREATE TABLE settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```

当前主要 key：

- `site_title`
- `logo_url`
- `theme`
- `agent_controller_url`
- `background_url`（兼容）
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
- `custom_code`：公开页面 CSS-only 外观扩展内容；前端只提取 `<style>` 或纯 CSS，不执行脚本/事件处理器。
- `admin_username`：单管理员账号名，默认 `admin`。
- `admin_password_hash`：单管理员密码 hash。首次部署未设置时可用 bootstrap admin token 登录；修改账号或密码后以后以这些设置为准。

## admin_sessions

后台单管理员登录 session。只存 session token hash，不存明文。

```sql
CREATE TABLE admin_sessions (
  token_hash TEXT PRIMARY KEY,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
);
```

## 迁移策略

`ensureSchema` 会在启动时创建缺失表，并通过 additive `ALTER TABLE ... ADD COLUMN` 补齐新增列。现阶段只做向前兼容 additive migration，不兼容旧系统 DB。
