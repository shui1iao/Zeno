# Home Card Spec / 首页服务器大卡片规格

首页大卡片是 Zeno 第一优先级展示目标。当前实现已经接入真实 Public API；本规格用于锁定卡片布局和展示语义。

## 可切换卡片主题

后台外观设置提供两种站点级卡片主题：

- `classic` / 经典卡片：默认值，完整保留升级前的卡片 DOM、信息密度和视觉效果。
- `capsule` / 节点舱：只调整服务器卡片内部，把硬件规格合并到四个资源格中；其余指标、颜色变量、背景、圆角、阴影、明暗主题和页面布局继续复用经典实现。

Public/Admin Settings 使用 `server_card_theme` 持久化，只接受 `classic` 或 `capsule`。旧数据库没有该字段、旧响应省略该字段或值非法时，必须安全回退到 `classic`。

## 桌面布局

- 服务器卡片网格：桌面端默认三列。
- 卡片之间留足间距，不挤、不像廉价小方块 dashboard。
- 卡片宽度自适应容器。
- 移动端单列，平板可两列。

建议断点：

```text
>= 1200px: 3 columns
768px - 1199px: 2 columns
< 768px: 1 column
```

## 卡片头部

头部只包含：

- OS 图标。
- 国家 / 地区旗帜。
- 服务器显示名。
- 右上角状态圆点与“在线 / 离线”文字；在线为绿色，离线为红色。

服务器名下不再单独展示 CPU 核心数、内存总量、硬盘总量、到期时间或续费金额；资源容量放到对应进度条下方，到期与续费信息放到底部指标区。

位置关系：

```text
[OS Icon]         [Flag] Server Name         [● 在线]
```

## 资源条

必须包含：

- CPU 使用率。
- 内存使用率 / 总量。
- 硬盘使用率 / 总量。
- 月流量使用率 / quota。

资源条固定为两列两行：第一行 CPU、内存，第二行硬盘、月流量。

每格结构：

```text
[icon] Label                  percent
[progress bar]
负载                         1m / 5m / 15m
占用                         used / capacity
```

进度条使用紧凑细条；CPU 标签前带 CPU 图标。CPU 条下左侧固定为“负载”，右侧为 1/5/15 分钟负载值；内存、硬盘、月流量条下左侧固定为“占用”，右侧为已用 / 总量。数值与单位之间保留空格，例如 `50 GB`、`128 KB/s`。

月流量条按当前计费周期的 `billable_bytes / quota_bytes` 计算，不按速度积分。

底部上传、下载、剩余、账单固定为两列两行；每项的图标与标签在左、数值在右，并保持单行：

```text
上传速度                 下载速度
剩余天数                 账单金额
```

延迟与丢包率位于底部独立历史区，使用紧凑小字号并各显示 12 小时状态条。

续费金额保留后台设置的账单周期，并按首页选择的金额单位换算；首页顶部月均消费与卡片账单金额必须使用同一汇率和币种选择。

颜色阈值建议：

- 0-69%：绿色。
- 70-84%：橙色。
- >=85%：红色。

月流量条按当前计费周期的 `billable_bytes / quota_bytes` 计算，不按速度积分；展示层要标注当前周期日期范围。

## 网络流量信息

必须展示：

- 当前下载速度。
- 当前上传速度。
- 总接收流量。
- 总发送流量。
- 本月计费流量。

展示层级：速度优先，总量次之。

## 延迟摘要

每张卡片需要展示当前选中的延迟目标摘要：

- target 名称。
- 最新 median 或 avg latency。
- loss percent。
- 状态颜色。

如果没有数据：显示 `No data`，不能伪造 0ms。

## 数据字段草案

```ts
interface HomeCardNode {
  id: string
  displayName: string
  status: 'online' | 'warning' | 'offline' | 'no_data'
  os: 'debian' | 'ubuntu' | 'centos' | 'alpine' | 'linux' | 'unknown'
  countryCode?: string
  subtitle?: string
  cpuPercent: number | null
  memoryUsedBytes: number | null
  memoryTotalBytes: number | null
  diskUsedBytes: number | null
  diskTotalBytes: number | null
  netInSpeedBps: number | null
  netOutSpeedBps: number | null
  netInTotalBytes: number | null
  netOutTotalBytes: number | null
  billingMode?: string
  monthlyResetDay?: number
  monthlyPeriodStart?: string
  monthlyPeriodEnd?: string
  monthlyBillableBytes: number | null
  monthlyQuotaBytes: number | null
  renewalAmount?: number | null
  renewalCurrency?: string
  billingCycle?: string
  monthlyCostCny?: number | null
  latencySummary?: LatencySummary
}

interface LatencySummary {
  targetId: string
  targetName: string
  medianMs: number | null
  avgMs: number | null
  lossPercent: number | null
  updatedAt: string
}
```
