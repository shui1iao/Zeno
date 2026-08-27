import { useEffect, useRef, type ReactNode } from 'react'
import type { HomeCardNode, HourlyLatencyPoint, NodeStatus, ServerCardTheme } from '../types'
import { formatLatency } from '../lib/format'
import { convertCurrencyAmount, formatCurrencyAmount, normalizeCurrencyCode, normalizeCurrencyRates, type CurrencyCode, type CurrencyRates } from '../lib/currency'
import { ServerFlag } from './ServerFlag'

interface ServerCardProps {
  node: HomeCardNode
  serverCardTheme?: ServerCardTheme
  displayCurrency?: CurrencyCode
  exchangeRates?: CurrencyRates
  onOpen?: (nodeId: string) => void
  onIntent?: (nodeId: string) => void
}

const osAsset: Record<string, string> = {
  debian: '/assets/logo/os-debian.svg',
  ubuntu: '/assets/logo/os-ubuntu.svg',
  windows: '/assets/logo/os-windows.svg',
  centos: '/assets/logo/linux.svg',
  alpine: '/assets/logo/linux.svg',
  linux: '/assets/logo/linux.svg',
  unknown: '/assets/logo/linux.svg',
}

function osAssetFor(os: string | undefined): string {
  const value = os?.trim().toLowerCase() ?? ''
  if (value.includes('windows')) return osAsset.windows
  if (value.includes('debian')) return osAsset.debian
  if (value.includes('ubuntu')) return osAsset.ubuntu
  if (value.includes('centos')) return osAsset.centos
  if (value.includes('alpine')) return osAsset.alpine
  if (value.includes('linux')) return osAsset.linux
  return osAsset.unknown
}

function ratio(used: number | null | undefined, total: number | null | undefined): number | null {
  if (used === null || used === undefined || total === null || total === undefined || total <= 0) return null
  return (used / total) * 100
}

function clampPercent(value: number | null | undefined): number {
  if (value === null || value === undefined || !Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, value))
}

function barTone(value: number | null | undefined): 'good' | 'warning' | 'danger' | 'empty' {
  if (value === null || value === undefined) return 'empty'
  if (value > 90) return 'danger'
  if (value > 60) return 'warning'
  return 'good'
}

function formatKulinBytes(value: number | null | undefined, options: { compact?: boolean } = {}): string {
  if (value === null || value === undefined) return '--'
  if (value === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = Math.abs(value)
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  const signed = value < 0 ? -size : size
  const digits = unit === 0 ? 0 : 2
  const joiner = options.compact ? '' : ' '
  return `${signed.toFixed(digits)}${joiner}${units[unit]}`
}

function formatRate(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${formatKulinBytes(value)}/s`
}

function formatCores(value: number | null | undefined): string {
  if (value === null || value === undefined) return '-- Cores'
  return `${value.toFixed(value % 1 === 0 ? 0 : 1)} ${value === 1 ? 'Core' : 'Cores'}`
}

function formatUsage(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return value.toFixed(2)
}

function formatLoad(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '--'
  return value.toFixed(2)
}

function formatOnlineDays(uptimeSeconds: number | null | undefined): string {
  if (uptimeSeconds === null || uptimeSeconds === undefined || !Number.isFinite(uptimeSeconds) || uptimeSeconds < 0) return '在线 -- 天'
  return `在线 ${Math.floor(uptimeSeconds / 86400)} 天`
}

function isOfflineStatus(status: NodeStatus | null | undefined): boolean {
  return status === 'offline' || status === 'no_data'
}

function normalizeLoss(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${value.toFixed(2)}%`
}

type HistoryKind = 'latency' | 'loss'
type HistoryTone = 'good' | 'warning' | 'danger' | 'empty'

function normalizeHourlyHistory(history: HourlyLatencyPoint[] | null | undefined): HourlyLatencyPoint[] {
  const values = (history ?? []).slice(-12)
  return [
    ...Array.from({ length: Math.max(0, 12 - values.length) }, () => ({ startedAt: '', latencyMs: null, lossPercent: null })),
    ...values,
  ]
}

function historyTone(kind: HistoryKind, value: number | null | undefined): HistoryTone {
  if (value === null || value === undefined || !Number.isFinite(value)) return 'empty'
  if (kind === 'latency') {
    if (value < 0) return 'empty'
    if (value >= 200) return 'danger'
    if (value >= 100) return 'warning'
    return 'good'
  }
  if (value >= 5) return 'danger'
  if (value >= 1) return 'warning'
  return 'good'
}

function historyTimestamp(value: string): string {
  const parsed = new Date(value)
  if (value.trim() === '' || Number.isNaN(parsed.getTime())) return '暂无数据'
  return parsed.toISOString().slice(0, 13).replace('T', ' ') + ':00'
}

function historyTitle(kind: HistoryKind, point: HourlyLatencyPoint): string {
  const value = kind === 'latency' ? point.latencyMs : point.lossPercent
  const label = kind === 'latency' ? '延迟' : '丢包'
  const suffix = kind === 'latency' ? 'ms' : '%'
  const separator = kind === 'latency' && value !== null && value !== undefined ? ' ' : ''
  return `${historyTimestamp(point.startedAt)} · ${label} ${value === null || value === undefined ? '--' : value.toFixed(2)}${separator}${suffix}`
}

function formatTrafficLabel(): string {
  return '流量'
}

function expiryBadge(expiryLabel: string | null | undefined): { text: string; tone: 'safe' | 'soon' | 'urgent' | 'expired' } | null {
  const trimmed = (expiryLabel ?? '').trim()
  if (trimmed === '') return null
  if (trimmed === '已过期') return { text: trimmed, tone: 'expired' }
  if (trimmed === '今天到期') return { text: '余 0 天', tone: 'urgent' }
  const relativeMatch = /^余\s*(\d+)\s*天$/.exec(trimmed)
  if (relativeMatch) {
    const days = Number(relativeMatch[1])
    if (days <= 7) return { text: `余 ${days} 天`, tone: 'urgent' }
    if (days <= 30) return { text: `余 ${days} 天`, tone: 'soon' }
    return { text: `余 ${days} 天`, tone: 'safe' }
  }
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(trimmed)
  if (!match) return trimmed === '永 久' || trimmed === '永久' ? { text: '永久', tone: 'safe' } : { text: trimmed, tone: 'safe' }
  const expiry = Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
  const now = new Date()
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate())
  const days = Math.ceil((expiry - today) / 86400000)
  if (days < 0) return { text: '已过期', tone: 'expired' }
  if (days === 0) return { text: '余 0 天', tone: 'urgent' }
  if (days <= 7) return { text: `余 ${days} 天`, tone: 'urgent' }
  if (days <= 30) return { text: `余 ${days} 天`, tone: 'soon' }
  return { text: `余 ${days} 天`, tone: 'safe' }
}

function expiryMetricValue(expiry: ReturnType<typeof expiryBadge>): string {
  if (!expiry) return '--'
  return expiry.text.replace(/^余\s*/, '')
}

function formatRenewalCost(amount: number | null | undefined, currency: string | null | undefined, cycle: string | null | undefined, displayCurrency: CurrencyCode, exchangeRates: CurrencyRates): string | null {
  if (amount === null || amount === undefined || !Number.isFinite(amount) || amount <= 0) return null
  const sourceCurrency = normalizeCurrencyCode(currency)
  const converted = convertCurrencyAmount(amount, sourceCurrency, displayCurrency, exchangeRates)
  const shownCurrency = converted === null ? sourceCurrency : displayCurrency
  const shownAmount = converted ?? amount
  const cycleText = (cycle ?? '').trim()
  return `${formatCurrencyAmount(shownAmount, shownCurrency, { spaced: true })}${cycleText ? ` / ${cycleText}` : ''}`
}

export function ServerCard({ node, serverCardTheme = 'classic', displayCurrency = 'CNY', exchangeRates: inputExchangeRates = { CNY: 1 }, onOpen, onIntent }: ServerCardProps) {
  const memoryPercent = ratio(node.memoryUsedBytes, node.memoryTotalBytes)
  const diskPercent = ratio(node.diskUsedBytes, node.diskTotalBytes)
  const trafficPercent = ratio(node.monthlyBillableBytes, node.monthlyQuotaBytes)
  const latency = node.latencySummary
  const hourlyHistory = normalizeHourlyHistory(latency?.hourlyHistory)
  const expiry = expiryBadge(node.expiryLabel)
  const exchangeRates = normalizeCurrencyRates(inputExchangeRates)
  const renewalCost = formatRenewalCost(node.renewalAmount, node.renewalCurrency, node.billingCycle, displayCurrency, exchangeRates)
  const isOfflineCard = isOfflineStatus(node.status)
  const capsule = serverCardTheme === 'capsule'
  const capsuleStatus = isOfflineCard ? 'status-offline' : node.status === 'warning' ? 'status-warning' : 'status-online'
  const intentTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const open = () => onOpen?.(node.id)
  const clearIntentTimer = () => {
    if (intentTimerRef.current === null) return
    clearTimeout(intentTimerRef.current)
    intentTimerRef.current = null
  }
  const runIntent = () => {
    clearIntentTimer()
    onIntent?.(node.id)
  }
  const scheduleIntent = () => {
    if (!onIntent || intentTimerRef.current !== null) return
    intentTimerRef.current = setTimeout(() => {
      intentTimerRef.current = null
      onIntent(node.id)
    }, 120)
  }

  useEffect(() => clearIntentTimer, [])

  return (
    <article
      className={`kulin-node-card${capsule ? ` is-capsule ${capsuleStatus}` : ''}${isOfflineCard ? ' is-offline' : ''}`}
      role={onOpen ? 'link' : undefined}
      tabIndex={onOpen ? 0 : undefined}
      onPointerEnter={scheduleIntent}
      onPointerLeave={clearIntentTimer}
      onPointerDown={runIntent}
      onFocus={runIntent}
      onBlur={clearIntentTimer}
      onClick={open}
      onKeyDown={(event) => {
        if (!onOpen) return
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault()
          open()
        }
      }}
    >
      <section className="node-head">
        <img alt={node.os || 'linux'} className="node-os" loading="lazy" src={osAssetFor(node.os)} />
        <div className="node-title-line">
          <ServerFlag countryCode={node.countryCode} className="node-flag" />
          <p>{node.displayName}</p>
        </div>
        <span className="node-uptime">{formatOnlineDays(node.uptimeSeconds)}</span>
      </section>

      {isOfflineCard && (
        <span className="node-offline-watermark" aria-label={`${node.displayName} 离线`}>
          离线
        </span>
      )}

      {!capsule && (
        <section className="node-specs" aria-label={`${node.displayName} specs`}>
          <SpecIcon kind="cpu" label={formatCores(node.cpuCores)} />
          <SpecIcon kind="memory" label={formatKulinBytes(node.memoryTotalBytes)} />
          <SpecIcon kind="disk" label={formatKulinBytes(node.diskTotalBytes)} />
        </section>
      )}

      <section className="node-usage" aria-label={`${node.displayName} usage`}>
        <div className="node-usage-grid">
          <UsageBar tone="cpu" label="CPU" valueText={`${formatUsage(node.cpuPercent)}%`} percent={node.cpuPercent} detail={capsule ? `${formatCores(node.cpuCores)} · ${formatLoad(node.load1)} / ${formatLoad(node.load5)} / ${formatLoad(node.load15)}` : undefined} />
          <UsageBar tone="memory" label="内存" valueText={`${formatUsage(memoryPercent)}%`} percent={memoryPercent} detail={capsule ? `${formatKulinBytes(node.memoryUsedBytes)} / ${formatKulinBytes(node.memoryTotalBytes)}` : undefined} />
          <UsageBar tone="disk" label="存储" valueText={`${formatUsage(diskPercent)}%`} percent={diskPercent} detail={capsule ? `${formatKulinBytes(node.diskUsedBytes)} / ${formatKulinBytes(node.diskTotalBytes)}` : undefined} />
          <UsageBar tone="traffic" label={formatTrafficLabel()} valueText={capsule ? `${formatUsage(trafficPercent)}%` : `${formatKulinBytes(node.monthlyBillableBytes, { compact: true })} / ${formatKulinBytes(node.monthlyQuotaBytes, { compact: true })}`} percent={trafficPercent} detail={capsule ? `${formatKulinBytes(node.monthlyBillableBytes)} / ${formatKulinBytes(node.monthlyQuotaBytes)}` : undefined} />
        </div>
        <section className="node-footer-grid" aria-label={`${node.displayName} network, billing, and health`}>
          <Metric tone="up" icon={<UploadIcon />} label="上传" value={formatRate(node.netOutSpeedBps)} />
          <Metric tone="down" icon={<DownloadIcon />} label="下载" value={formatRate(node.netInSpeedBps)} />
          <Metric tone="expiry" stateTone={expiry?.tone} icon={<CalendarIcon />} label="剩余" value={expiryMetricValue(expiry)} />
          <Metric tone="billing" icon={<WalletIcon />} label="账单" value={renewalCost ?? '--'} />
          <HealthHistoryRow kind="latency" icon={<ActivityIcon />} label="延迟" value={latency?.avgMs != null ? formatLatency(latency.avgMs) : '--ms'} points={hourlyHistory} />
          <HealthHistoryRow kind="loss" icon={<TriangleAlertIcon />} label="丢包率" value={latency ? normalizeLoss(latency.lossPercent) : '--%'} points={hourlyHistory} />
        </section>
      </section>
    </article>
  )
}

type ResourceTone = 'cpu' | 'memory' | 'disk' | 'traffic'

function UsageBar({ tone, label, valueText, percent, detail }: { tone: ResourceTone; label: string; valueText: string; percent: number | null | undefined; detail?: string }) {
  const value = clampPercent(percent)
  return (
    <div className={`usage-row usage-row--${tone}`}>
      <div className="usage-row__meta">
        <span className="usage-row__label">
          <span>{label}</span>
        </span>
        <strong>{valueText}</strong>
      </div>
      <div className="usage-track" role="progressbar" aria-label={`${label} usage`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={value}>
        <div className={`usage-fill is-${barTone(percent)}`} style={{ transform: `translateX(-${100 - value}%)` }} />
      </div>
      {detail !== undefined && <span className="usage-row__detail">{detail}</span>}
    </div>
  )
}

type MetricTone = 'up' | 'down' | 'expiry' | 'billing'
type ExpiryTone = 'safe' | 'soon' | 'urgent' | 'expired'

function HealthHistoryRow({ kind, icon, label, value, points }: { kind: HistoryKind; icon: ReactNode; label: string; value: string; points: HourlyLatencyPoint[] }) {
  return (
    <div className={`node-health-metric health-${kind}`}>
      <div className="health-history-heading">
        <span className="metric-heading">
          <span className="metric-icon">{icon}</span>
          <span className="metric-label">{label}</span>
        </span>
        <strong>{value}</strong>
      </div>
      <div className="health-history-strip" aria-label={`${label} 12小时趋势`}>
        {points.map((point, index) => {
          const metricValue = kind === 'latency' ? point.latencyMs : point.lossPercent
          return (
            <span
              key={`${point.startedAt || 'empty'}-${index}`}
              className={`history-cell history-${kind} is-${historyTone(kind, metricValue)}`}
              title={historyTitle(kind, point)}
              aria-label={historyTitle(kind, point)}
            />
          )
        })}
      </div>
    </div>
  )
}

function Metric({ tone, stateTone, icon, label, value }: { tone: MetricTone; stateTone?: ExpiryTone; icon: ReactNode; label: string; value: string }) {
  return (
    <div className={`node-metric metric-${tone}${stateTone ? ` is-${stateTone}` : ''}`}>
      <span className="metric-heading">
        <span className="metric-icon">{icon}</span>
        <span className="metric-label">{label}</span>
      </span>
      <strong>{value}</strong>
    </div>
  )
}

function SpecIcon({ kind, label }: { kind: 'cpu' | 'memory' | 'disk'; label: string }) {
  return (
    <div className={`node-spec spec-${kind}`}>
      <svg viewBox="0 0 24 24" aria-hidden="true">
        {kind === 'cpu' && <><rect x="5" y="5" width="14" height="14" rx="2" /><rect x="9" y="9" width="6" height="6" rx="1" /><path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3" /></>}
        {kind === 'memory' && <><rect x="3" y="7" width="18" height="10" rx="2" /><path d="M7 11v2M11 11v2M15 11v2M19 11v2M5 17v3M9 17v3M15 17v3M19 17v3" /></>}
        {kind === 'disk' && <><path d="M5 5h14l3 7v5a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-5l3-7Z" /><path d="M3 12h18" /><circle cx="7" cy="16" r="1" /><circle cx="11" cy="16" r="1" /></>}
      </svg>
      <span>{label}</span>
    </div>
  )
}

function UploadIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 3v12" />
      <path d="m17 8-5-5-5 5" />
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
    </svg>
  )
}

function DownloadIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 15V3" />
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <path d="m7 10 5 5 5-5" />
    </svg>
  )
}

function ActivityIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2" />
    </svg>
  )
}

function TriangleAlertIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </svg>
  )
}

function CalendarIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M8 2v4M16 2v4M3 9h18" />
      <rect x="3" y="4" width="18" height="17" rx="2" />
    </svg>
  )
}

function WalletIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M20 7V6a2 2 0 0 0-2-2H5a3 3 0 0 0 0 6h15v10H5a3 3 0 0 1-3-3V7" />
      <path d="M16 14h4" />
    </svg>
  )
}
