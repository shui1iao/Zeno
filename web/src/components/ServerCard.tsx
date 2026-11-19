import { memo, useEffect, useRef, type KeyboardEvent, type ReactNode } from 'react'
import type { HomeCardNode, HourlyLatencyPoint, ServerCardTheme } from '../types'
import { formatLatency } from '../lib/format'
import { convertCurrencyAmount, formatCurrencyAmount, normalizeCurrencyCode, normalizeCurrencyRates, type CurrencyCode, type CurrencyRates } from '../lib/currency'
import { ServerFlag } from './ServerFlag'
import { expiryBadge, isOfflineStatus } from '../lib/nodeHealth'

export { expiryBadge, formatLatency, isOfflineStatus, normalizeCurrencyRates, ServerFlag }

export interface ServerCardProps {
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
  darwin: '/assets/logo/os-macos.svg',
  centos: '/assets/logo/linux.svg',
  alpine: '/assets/logo/linux.svg',
  linux: '/assets/logo/linux.svg',
  unknown: '/assets/logo/linux.svg',
}

export function osKindFor(os: string | undefined): string {
  const value = os?.trim().toLowerCase() ?? ''
  if (value.includes('darwin') || value.includes('macos') || value.includes('mac os')) return 'darwin'
  for (const kind of ['windows', 'debian', 'ubuntu', 'centos', 'alpine', 'linux']) {
    if (value.includes(kind)) return kind
  }
  return 'unknown'
}

export function osAssetFor(os: string | undefined): string {
  const value = os?.trim().toLowerCase() ?? ''
  if (value.includes('darwin') || value.includes('macos') || value.includes('mac os')) return osAsset.darwin
  if (value.includes('windows')) return osAsset.windows
  if (value.includes('debian')) return osAsset.debian
  if (value.includes('ubuntu')) return osAsset.ubuntu
  if (value.includes('centos')) return osAsset.centos
  if (value.includes('alpine')) return osAsset.alpine
  if (value.includes('linux')) return osAsset.linux
  return osAsset.unknown
}

export function ratio(used: number | null | undefined, total: number | null | undefined): number | null {
  if (used === null || used === undefined || total === null || total === undefined || total <= 0) return null
  return (used / total) * 100
}

export function clampPercent(value: number | null | undefined): number {
  if (value === null || value === undefined || !Number.isFinite(value)) return 0
  return Math.max(0, Math.min(100, value))
}

export function barTone(value: number | null | undefined): 'good' | 'warning' | 'danger' | 'empty' {
  if (value === null || value === undefined) return 'empty'
  if (value > 90) return 'danger'
  if (value > 60) return 'warning'
  return 'good'
}

export function formatKulinBytes(value: number | null | undefined, options: { total?: boolean } = {}): string {
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
  const amount = signed.toFixed(digits)
  // Configured totals (quota, memory, disk) are usually whole numbers: 50 TB, not 50.00 TB.
  return `${options.total ? amount.replace(/\.00$/, '') : amount} ${units[unit]}`
}

export function formatRate(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${formatKulinBytes(value)}/s`
}

function formatCores(value: number | null | undefined): string {
  if (value === null || value === undefined) return '-- Cores'
  return `${value.toFixed(value % 1 === 0 ? 0 : 1)} ${value === 1 ? 'Core' : 'Cores'}`
}

export function formatUsage(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return value.toFixed(2)
}

function formatLoad(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '--'
  return value.toFixed(2)
}

export function formatOnlineDays(uptimeSeconds: number | null | undefined): string {
  if (uptimeSeconds === null || uptimeSeconds === undefined || !Number.isFinite(uptimeSeconds) || uptimeSeconds < 0) return '在线 -- 天'
  return `在线 ${Math.floor(uptimeSeconds / 86400)} 天`
}

export function normalizeLoss(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${value.toFixed(2)}%`
}

type HistoryKind = 'latency' | 'loss'
type HistoryTone = 'good' | 'warning' | 'danger' | 'empty'

export function normalizeHourlyHistory(history: HourlyLatencyPoint[] | null | undefined): HourlyLatencyPoint[] {
  const values = (history ?? []).slice(-12)
  return [
    ...Array.from({ length: Math.max(0, 12 - values.length) }, () => ({ startedAt: '', latencyMs: null, lossPercent: null })),
    ...values,
  ]
}

export function historyTone(kind: HistoryKind, value: number | null | undefined): HistoryTone {
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
  const pad = (part: number) => String(part).padStart(2, '0')
  return `${parsed.getFullYear()}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())} ${pad(parsed.getHours())}:00`
}

export function historyTitle(kind: HistoryKind, point: HourlyLatencyPoint): string {
  const value = kind === 'latency' ? point.latencyMs : point.lossPercent
  const label = kind === 'latency' ? '延迟' : '丢包'
  const suffix = kind === 'latency' ? 'ms' : '%'
  if (value === null || value === undefined) return `${historyTimestamp(point.startedAt)} · ${label} --`
  const separator = kind === 'latency' ? ' ' : ''
  return `${historyTimestamp(point.startedAt)} · ${label} ${value.toFixed(2)}${separator}${suffix}`
}

export function historyStripSummary(kind: HistoryKind, label: string, points: HourlyLatencyPoint[]): string {
  const values = points.map((point) => kind === 'latency' ? point.latencyMs : point.lossPercent)
  const counts = { good: 0, warning: 0, danger: 0, empty: 0 }
  values.forEach((value) => { counts[historyTone(kind, value)] += 1 })
  return `${label}近 12 小时：正常 ${counts.good} 小时，偏高 ${counts.warning} 小时，严重 ${counts.danger} 小时，无数据 ${counts.empty} 小时`
}

function formatTrafficLabel(): string {
  return '流量'
}

export function expiryMetricValue(expiry: ReturnType<typeof expiryBadge>): string {
  if (!expiry) return '--'
  return expiry.text.replace(/^余\s*/, '')
}

export function formatRenewalCost(amount: number | null | undefined, currency: string | null | undefined, cycle: string | null | undefined, displayCurrency: CurrencyCode, exchangeRates: CurrencyRates): string | null {
  if (amount === null || amount === undefined || !Number.isFinite(amount) || amount <= 0) return null
  const sourceCurrency = normalizeCurrencyCode(currency)
  const converted = convertCurrencyAmount(amount, sourceCurrency, displayCurrency, exchangeRates)
  const shownCurrency = converted === null ? sourceCurrency : displayCurrency
  const shownAmount = converted ?? amount
  const cycleText = (cycle ?? '').trim()
  return `${formatCurrencyAmount(shownAmount, shownCurrency, { spaced: true })}${cycleText ? ` / ${cycleText}` : ''}`
}

function sameHourlyHistory(left?: HourlyLatencyPoint[], right?: HourlyLatencyPoint[]): boolean {
  if (left === right) return true
  if (!left || !right || left.length !== right.length) return false
  for (let i = 0; i < left.length; i += 1) {
    if (left[i].startedAt !== right[i].startedAt || left[i].latencyMs !== right[i].latencyMs || left[i].lossPercent !== right[i].lossPercent) {
      return false
    }
  }
  return true
}

function sameCurrencyRates(left?: CurrencyRates, right?: CurrencyRates): boolean {
  if (left === right) return true
  if (!left || !right) return false
  const keys = Object.keys(left) as CurrencyCode[]
  if (keys.length !== Object.keys(right).length) return false
  return keys.every((key) => left[key] === right[key])
}

export function sameServerCardProps(prev: ServerCardProps, next: ServerCardProps): boolean {
  if (prev.serverCardTheme !== next.serverCardTheme || prev.displayCurrency !== next.displayCurrency || prev.onOpen !== next.onOpen || prev.onIntent !== next.onIntent) {
    return false
  }
  if (!sameCurrencyRates(prev.exchangeRates, next.exchangeRates)) return false
  const a = prev.node
  const b = next.node
  if (a === b) return true
  return (
    a.id === b.id &&
    a.displayName === b.displayName &&
    a.status === b.status &&
    a.os === b.os &&
    a.countryCode === b.countryCode &&
    a.cpuCores === b.cpuCores &&
    a.cpuPercent === b.cpuPercent &&
    a.load1 === b.load1 &&
    a.load5 === b.load5 &&
    a.load15 === b.load15 &&
    a.memoryUsedBytes === b.memoryUsedBytes &&
    a.memoryTotalBytes === b.memoryTotalBytes &&
    a.diskUsedBytes === b.diskUsedBytes &&
    a.diskTotalBytes === b.diskTotalBytes &&
    a.monthlyBillableBytes === b.monthlyBillableBytes &&
    a.monthlyQuotaBytes === b.monthlyQuotaBytes &&
    a.netInSpeedBps === b.netInSpeedBps &&
    a.netOutSpeedBps === b.netOutSpeedBps &&
    a.uptimeSeconds === b.uptimeSeconds &&
    a.expiryLabel === b.expiryLabel &&
    a.renewalAmount === b.renewalAmount &&
    a.renewalCurrency === b.renewalCurrency &&
    a.billingCycle === b.billingCycle &&
    a.latencySummary?.avgMs === b.latencySummary?.avgMs &&
    a.latencySummary?.lossPercent === b.latencySummary?.lossPercent &&
    sameHourlyHistory(a.latencySummary?.hourlyHistory, b.latencySummary?.hourlyHistory)
  )
}

export const ServerCard = memo(function ServerCard({ node, serverCardTheme = 'classic', displayCurrency = 'CNY', exchangeRates: inputExchangeRates = { CNY: 1 }, onOpen, onIntent }: ServerCardProps) {
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
  const openHandlers = useNodeOpenHandlers(node.id, onOpen, onIntent)

  return (
    <div
      className={`kulin-node-card${capsule ? ` is-capsule ${capsuleStatus}` : ''}${isOfflineCard ? ' is-offline' : ''}`}
      {...openHandlers}
    >
      <section className="node-head">
        <img alt={node.os || 'linux'} className="node-os" data-os={osKindFor(node.os)} src={osAssetFor(node.os)} />
        <div className="node-title-line">
          <ServerFlag countryCode={node.countryCode} className="node-flag" />
          <p>{node.displayName}</p>
        </div>
        <span className="node-uptime">{isOfflineCard ? '离线' : formatOnlineDays(node.uptimeSeconds)}</span>
      </section>

      {isOfflineCard && (
        <span className="node-offline-watermark" aria-label={`${node.displayName} 离线`}>
          离线
        </span>
      )}

      {!capsule && (
        <section className="node-specs" aria-label={`${node.displayName} specs`}>
          <SpecIcon kind="cpu" label={formatCores(node.cpuCores)} />
          <SpecIcon kind="memory" label={formatKulinBytes(node.memoryTotalBytes, { total: true })} />
          <SpecIcon kind="disk" label={formatKulinBytes(node.diskTotalBytes, { total: true })} />
        </section>
      )}

      <section className="node-usage" aria-label={`${node.displayName} usage`}>
        <div className="node-usage-grid">
          <UsageBar tone="cpu" label="CPU" valueText={`${formatUsage(node.cpuPercent)}%`} percent={node.cpuPercent} detail={capsule ? `${formatCores(node.cpuCores)} · ${formatLoad(node.load1)} / ${formatLoad(node.load5)} / ${formatLoad(node.load15)}` : undefined} />
          <UsageBar tone="memory" label="内存" valueText={`${formatUsage(memoryPercent)}%`} percent={memoryPercent} detail={capsule ? `${formatKulinBytes(node.memoryUsedBytes)} / ${formatKulinBytes(node.memoryTotalBytes, { total: true })}` : undefined} />
          <UsageBar tone="disk" label="存储" valueText={`${formatUsage(diskPercent)}%`} percent={diskPercent} detail={capsule ? `${formatKulinBytes(node.diskUsedBytes)} / ${formatKulinBytes(node.diskTotalBytes, { total: true })}` : undefined} />
          <UsageBar tone="traffic" label={formatTrafficLabel()} valueText={capsule ? `${formatUsage(trafficPercent)}%` : `${formatKulinBytes(node.monthlyBillableBytes)} / ${formatKulinBytes(node.monthlyQuotaBytes, { total: true })}`} percent={trafficPercent} detail={capsule ? `${formatKulinBytes(node.monthlyBillableBytes)} / ${formatKulinBytes(node.monthlyQuotaBytes, { total: true })}` : undefined} />
        </div>
        <section className="node-footer-grid" aria-label={`${node.displayName} network, billing, and health`}>
          <Metric tone="up" icon={<UploadIcon />} label="上传" value={formatRate(node.netOutSpeedBps)} />
          <Metric tone="down" icon={<DownloadIcon />} label="下载" value={formatRate(node.netInSpeedBps)} />
          <Metric tone="expiry" icon={<CalendarIcon />} label="剩余" value={expiryMetricValue(expiry)} />
          <Metric tone="billing" icon={<WalletIcon />} label="账单" value={renewalCost ?? '--'} />
          <HealthHistoryRow kind="latency" icon={<ActivityIcon />} label="延迟" value={formatLatency(latency?.avgMs)} points={hourlyHistory} />
          <HealthHistoryRow kind="loss" icon={<TriangleAlertIcon />} label="丢包率" value={latency ? normalizeLoss(latency.lossPercent) : '--'} points={hourlyHistory} />
        </section>
      </section>
    </div>
  )
}, sameServerCardProps)


/** Shared open/prefetch behaviour for home cards and list rows. */
export function useNodeOpenHandlers(nodeId: string, onOpen?: (nodeId: string) => void, onIntent?: (nodeId: string) => void) {
  const intentTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const clearIntentTimer = () => {
    if (intentTimerRef.current === null) return
    clearTimeout(intentTimerRef.current)
    intentTimerRef.current = null
  }
  useEffect(() => clearIntentTimer, [])
  const open = () => onOpen?.(nodeId)
  const runIntent = () => {
    clearIntentTimer()
    onIntent?.(nodeId)
  }
  return {
    role: onOpen ? 'link' : undefined,
    tabIndex: onOpen ? 0 : undefined,
    onPointerEnter: () => {
      if (!onIntent || intentTimerRef.current !== null) return
      intentTimerRef.current = setTimeout(() => {
        intentTimerRef.current = null
        onIntent(nodeId)
      }, 120)
    },
    onPointerLeave: clearIntentTimer,
    onPointerDown: runIntent,
    onFocus: runIntent,
    onBlur: clearIntentTimer,
    onClick: open,
    onKeyDown: (event: KeyboardEvent<HTMLElement>) => {
      if (!onOpen) return
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault()
        open()
      }
    },
  }
}

type ResourceTone = 'cpu' | 'memory' | 'disk' | 'traffic'

function UsageBar({ tone, label, valueText, percent, detail }: { tone: ResourceTone; label: string; valueText: string; percent: number | null | undefined; detail?: string }) {
  const value = clampPercent(percent)
  const fillPercent = value > 0 && value < 2.8 ? 2.8 : value
  return (
    <div className={`usage-row usage-row--${tone}`}>
      <div className="usage-row__meta">
        <span className="usage-row__label">
          <span>{label}</span>
        </span>
        <strong>{valueText}</strong>
      </div>
      <div className="usage-track" role="progressbar" aria-label={`${label} usage`} aria-valuemin={0} aria-valuemax={100} aria-valuenow={value}>
        <div className={`usage-fill is-${barTone(percent)}`} style={{ transform: `translateX(-${100 - fillPercent}%)` }} />
      </div>
      {detail !== undefined && <span className="usage-row__detail">{detail}</span>}
    </div>
  )
}

type MetricTone = 'up' | 'down' | 'expiry' | 'billing'

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
      <div className="health-history-strip" role="img" aria-label={historyStripSummary(kind, label, points)}>
        {points.map((point, index) => {
          const metricValue = kind === 'latency' ? point.latencyMs : point.lossPercent
          return (
            <span
              key={`${point.startedAt || 'empty'}-${index}`}
              className={`history-cell history-${kind} is-${historyTone(kind, metricValue)}`}
              title={historyTitle(kind, point)}
            />
          )
        })}
      </div>
    </div>
  )
}

function Metric({ tone, icon, label, value }: { tone: MetricTone; icon: ReactNode; label: string; value: string }) {
  return (
    <div className={`node-metric metric-${tone}`}>
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
