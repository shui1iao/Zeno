import { useMemo, useState } from 'react'
import type { StatePoint } from '../types'
import { formatBps, formatBytes, formatPercent, formatTooltipDateTime, missingValue, scaleBytes } from '../lib/format'
import { finiteOrNull } from '../lib/number'
import { availableHistoryRanges } from '../lib/historyRange'
import { lastFiniteIndex, maxFinite, sortedByTime, type ResourceScale } from '../lib/resourceChart'
import { HistoryRangeSelector } from './HistoryRangeSelector'
import { SmoothingSelector, type SmoothingMode } from './SmoothingSelector'
import { smoothStatePoints } from '../lib/smoothStatePoints'
import { ResourceMiniChart, type ResourceChartExtraRow, type ResourceChartSeries } from './ResourceChart'

interface StateHistoryPanelProps {
  points: StatePoint[]
  range: string
  loading?: boolean
  error?: string
  canUseExtendedRanges?: boolean
  onRangeChange?: (range: string) => void
}

export interface ResourceMetric {
  key: string
  title: string
  scale: ResourceScale
  series: ResourceChartSeries[]
  extraRows?: ResourceChartExtraRow[]
  /** Big number on the metric tile (latest sample). */
  value: string
  /** Second line on the metric tile. */
  detail: string
  /** Optional third line, e.g. Swap under memory. */
  extra?: string
  /** Longer form of `extra` for the tile title. */
  extraTitle?: string
}

export interface ResourceReadout {
  value: string
  detail: string
}

/** Header reading of one small chart: the latest sample, or the hovered one. */
export function resourceReadout(metric: ResourceMetric, index: number | null): ResourceReadout {
  if (index === null) {
    return { value: metric.value, detail: [metric.detail, metric.extra].filter(Boolean).join(' · ') }
  }
  const describe = (key: string) => metric.series.find((item) => item.key === key)?.describe(index) ?? missingValue
  if (metric.key === 'network') return { value: `↓ ${describe('net-in')}`, detail: `↑ ${describe('net-out')}` }
  if (metric.key === 'connections') return { value: `${describe('tcp')} / ${describe('udp')}`, detail: '' }
  const [first, ...rest] = (metric.series[0]?.describe(index) ?? missingValue).split(' · ')
  const extras = [
    ...rest,
    ...metric.series.slice(1).map((item) => `${item.label} ${item.describe(index)}`),
    ...(metric.extraRows ?? []).map((row) => {
      const text = row.describe(index)
      return text === null ? '' : `${row.label} ${text}`
    }),
  ].filter(Boolean)
  return { value: first, detail: extras.join(' · ') }
}

export function StateHistoryPanel({ points, range, loading = false, error, canUseExtendedRanges = false, onRangeChange = () => {} }: StateHistoryPanelProps) {
  const rangeOptions = availableHistoryRanges(canUseExtendedRanges)
  const sorted = useMemo(() => sortStatePoints(points), [points])
  const timestamps = useMemo(() => sorted.map((point) => Date.parse(point.ts)), [sorted])
  const metrics = useMemo(() => buildResourceMetrics(sorted), [sorted])
  // Unlike the latency chart, the resource charts open smoothed. The readings
  // keep the real values; only the drawn lines change.
  const [smoothing, setSmoothing] = useState<SmoothingMode>('smooth')
  const chartMetrics = useMemo(
    () => (smoothing === 'smooth' ? buildResourceMetrics(smoothStatePoints(sorted)) : metrics),
    [smoothing, sorted, metrics],
  )
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const activeHoverIndex = hoverIndex !== null && hoverIndex < timestamps.length ? hoverIndex : null
  const sampleCount = sorted.length

  return (
    <section className="monitor-panel resource-history-panel" aria-label="agent state history">
      <header className="resource-history-header">
        <div className="monitor-title-row">
          <h3>系统资源</h3>
          <SmoothingSelector value={smoothing} onChange={setSmoothing} ariaLabel="资源曲线平滑" />
          {sampleCount > 0 && <span className="resource-history-meta">{activeHoverIndex === null ? '当前读数' : `${formatTooltipDateTime(timestamps[activeHoverIndex])} 的读数`}</span>}
        </div>
        <HistoryRangeSelector
          ariaLabel="resource history range selector"
          options={rangeOptions}
          value={range}
          onChange={onRangeChange}
          className="resource-range-row"
        />
      </header>

      {loading && <div className="detail-state" data-state="loading" role="status" aria-live="polite">正在读取系统资源…</div>}
      {error && <div className="detail-state is-error" data-state="error" role="alert">系统资源读取失败：{error}</div>}
      {!loading && !error && sampleCount === 0 && <div className="detail-state" data-state="empty" role="status" aria-live="polite">暂无系统资源趋势数据</div>}

      {!loading && !error && sampleCount > 0 && (
        <div className="resource-multiples" role="group" aria-label="resource metrics">
          {metrics.map((metric, index) => {
            const chartMetric = chartMetrics[index] ?? metric
            const readout = resourceReadout(metric, activeHoverIndex)
            return (
              <article key={metric.key} className="resource-multiple" data-metric={metric.key} title={[metric.title, metric.value, metric.detail, metric.extraTitle || metric.extra].filter(Boolean).join(' · ')}>
                <header className="resource-multiple__head">
                  <span className="resource-multiple__title">
                    <i style={{ backgroundColor: metric.series[0]?.color }} aria-hidden="true" />
                    {metric.title}
                  </span>
                  <strong className="resource-multiple__value">{readout.value}</strong>
                </header>
                <div className="resource-multiple__detail">
                  {metric.series.length > 1 && (
                    <span className="resource-multiple__legend">
                      {metric.series.map((item) => <span key={item.key}><i className={item.key === 'swap' ? 'is-dashed' : undefined} style={{ color: item.color }} />{item.label}</span>)}
                    </span>
                  )}
                  <span className="resource-multiple__text">{readout.detail || '\u00a0'}</span>
                </div>
                <ResourceMiniChart
                  label={metric.title}
                  scale={chartMetric.scale}
                  series={chartMetric.series}
                  timestamps={timestamps}
                  hoverIndex={activeHoverIndex}
                  onHoverIndexChange={setHoverIndex}
                />
              </article>
            )
          })}
        </div>
      )}
    </section>
  )
}

interface ChartView {
  key: string
  label: string
  scale: ResourceScale
  series: ResourceChartSeries[]
  extraRows: ResourceChartExtraRow[]
}

/**
 * Metrics sharing one unit keep a real axis. A mixed view uses a 0-100 axis:
 * percentages stay as they are, while rates and counts are rescaled to their
 * own metric's peak (both lines of a pair share that peak). Tooltips still
 * report the real values.
 */
export function buildChartView(metrics: ResourceMetric[], selectedKeys: string[]): ChartView {
  const visible = selectedKeys.length > 0 ? metrics.filter((metric) => selectedKeys.includes(metric.key)) : metrics
  const scales = new Set(visible.map((metric) => metric.scale))
  const mixed = scales.size > 1
  const series = visible.flatMap((metric) => {
    if (!mixed || metric.scale === 'percent') return metric.series
    const peak = maxFinite(metric.series.flatMap((item) => item.values)) ?? 0
    return metric.series.map((item) => ({
      ...item,
      plot: item.values.map((value) => (value === null ? null : peak > 0 ? (value / peak) * 100 : 0)),
    }))
  })
  return {
    key: visible.map((metric) => metric.key).join('+'),
    label: visible.map((metric) => metric.title).join(' / '),
    scale: mixed ? 'percent' : visible[0]?.scale ?? 'percent',
    series,
    extraRows: visible.flatMap((metric) => metric.extraRows ?? []),
  }
}

function sortStatePoints(points: StatePoint[]): StatePoint[] {
  return sortedByTime(points.filter((point) => Number.isFinite(Date.parse(point.ts))), (point) => Date.parse(point.ts))
}

function latestOf(values: Array<number | null>, format: (index: number) => string): string {
  const index = lastFiniteIndex(values)
  return index >= 0 ? format(index) : missingValue
}

function buildResourceMetrics(points: StatePoint[]): ResourceMetric[] {
  const read = (pick: (point: StatePoint) => number | null | undefined) => points.map((point) => finiteOrNull(pick(point)))
  const cpu = read((point) => point.cpuPercent)
  const memory = points.map((point) => ratioPercent(point.memoryUsedBytes, point.memoryTotalBytes))
  const swap = points.map((point) => ratioPercent(point.swapUsedBytes, point.swapTotalBytes))
  const disk = points.map((point) => ratioPercent(point.diskUsedBytes, point.diskTotalBytes))
  const netOut = read((point) => point.netOutSpeedBps)
  const netIn = read((point) => point.netInSpeedBps)
  const processes = read((point) => point.processCount)
  const tcp = read((point) => point.tcpConnectionCount)
  const udp = read((point) => point.udpConnectionCount)
  const hasSwap = swap.some((value) => value !== null)

  const percentAt = (values: Array<number | null>) => (index: number) => formatPercent(values[index])
  const usageAt = (values: Array<number | null>, used: (point: StatePoint) => number | null, total: (point: StatePoint) => number | null) => (index: number) => {
    const pair = formatResourceValue(used(points[index]), total(points[index]))
    const percent = formatPercent(values[index])
    return pair === missingValue ? percent : `${percent} · ${pair}`
  }
  const rateAt = (values: Array<number | null>) => (index: number) => formatBps(values[index])
  const countAt = (values: Array<number | null>) => (index: number) => formatCount(values[index])
  const latestPoint = (pick: (point: StatePoint) => string | null): string => {
    for (let index = points.length - 1; index >= 0; index -= 1) {
      const text = pick(points[index])
      if (text !== null) return text
    }
    return ''
  }

  const memoryDescribe = usageAt(memory, (point) => point.memoryUsedBytes, (point) => point.memoryTotalBytes)
  const memorySeries: ResourceChartSeries[] = [{ key: 'memory', label: '内存', color: 'var(--metric-blue)', values: memory, describe: memoryDescribe }]
  if (hasSwap) {
    memorySeries.push({ key: 'swap', label: 'Swap', color: '#38bdf8', values: swap, describe: usageAt(swap, (point) => point.swapUsedBytes, (point) => point.swapTotalBytes) })
  }
  const diskDescribe = usageAt(disk, (point) => point.diskUsedBytes, (point) => point.diskTotalBytes)

  return [
    {
      key: 'cpu',
      title: 'CPU',
      scale: 'percent',
      series: [{ key: 'cpu', label: 'CPU', color: 'var(--metric-green)', values: cpu, describe: percentAt(cpu) }],
      extraRows: [{ label: '负载', describe: (index) => formatLoad(points[index]) }],
      value: latestOf(cpu, percentAt(cpu)),
      detail: latestPoint(formatLoad),
    },
    {
      key: 'memory',
      title: '内存',
      scale: 'percent',
      series: memorySeries,
      value: latestOf(memory, percentAt(memory)),
      detail: latestPoint((point) => { const pair = formatResourceValue(point.memoryUsedBytes, point.memoryTotalBytes); return pair === missingValue ? null : pair }),
      // The tile shows the usage; the hover/long-press title and the chart
      // tooltip add the percentage.
      extra: hasSwap ? latestPoint((point) => formatSwap(point, false)) : '',
      extraTitle: hasSwap ? latestPoint((point) => formatSwap(point, true)) : '',
    },
    {
      key: 'disk',
      title: '磁盘',
      scale: 'percent',
      series: [{ key: 'disk', label: '磁盘', color: 'var(--metric-purple)', values: disk, describe: diskDescribe }],
      value: latestOf(disk, percentAt(disk)),
      detail: latestPoint((point) => { const pair = formatResourceValue(point.diskUsedBytes, point.diskTotalBytes); return pair === missingValue ? null : pair }),
    },
    {
      key: 'network',
      title: '网络',
      scale: 'rate',
      series: [
        { key: 'net-out', label: '上传', color: 'var(--metric-orange)', values: netOut, describe: rateAt(netOut) },
        { key: 'net-in', label: '下载', color: '#eab308', values: netIn, describe: rateAt(netIn) },
      ],
      value: `↓ ${latestOf(netIn, rateAt(netIn))}`,
      detail: `↑ ${latestOf(netOut, rateAt(netOut))}`,
    },
    {
      key: 'processes',
      title: '进程数',
      scale: 'count',
      series: [{ key: 'processes', label: '进程', color: '#84cc16', values: processes, describe: countAt(processes) }],
      value: latestOf(processes, countAt(processes)),
      detail: '',
    },
    {
      key: 'connections',
      title: 'TCP / UDP',
      scale: 'count',
      series: [
        { key: 'tcp', label: 'TCP', color: '#ec4899', values: tcp, describe: countAt(tcp) },
        { key: 'udp', label: 'UDP', color: '#94a3b8', values: udp, describe: countAt(udp) },
      ],
      value: `${latestOf(tcp, countAt(tcp))} / ${latestOf(udp, countAt(udp))}`,
      detail: '',
    },
  ]
}

function formatSwap(point: StatePoint, withPercent: boolean): string | null {
  const percent = ratioPercent(point.swapUsedBytes, point.swapTotalBytes)
  if (percent === null) return null
  const pair = compactBytePair(point.swapUsedBytes, point.swapTotalBytes)
  return withPercent ? `Swap ${formatPercent(percent)} · ${pair}` : `Swap ${pair}`
}

/**
 * Narrow-tile form of used / total: the used side is written in the total's
 * unit, so the unit appears once ("1.1 / 2.0 GB", "0.14 / 2.0 GB").
 */
function compactBytePair(used: number | null, total: number | null): string {
  const safeUsed = finiteOrNull(used)
  const safeTotal = finiteOrNull(total)
  if (safeUsed === null || safeTotal === null || safeTotal <= 0) return formatResourceValue(used, total)
  const totalText = formatBytes(safeTotal)
  // Same unit as the total's label.
  const { power } = scaleBytes(safeTotal)
  const scaled = safeUsed / 1024 ** power
  const usedText = power === 0 ? String(Math.round(scaled)) : scaled > 0 && scaled < 1 ? scaled.toFixed(2) : scaled.toFixed(1)
  return `${usedText} / ${totalText}`
}

function formatLoad(point: StatePoint): string | null {
  const values = [point.load1, point.load5, point.load15].map(finiteOrNull)
  if (values.some((value) => value === null)) return null
  return values.map((value) => (value as number).toFixed(2)).join(' / ')
}

function formatCount(value: number | null): string {
  return value === null ? missingValue : String(Math.round(value))
}

export function formatResourceUsage(used: number | null | undefined, total: number | null | undefined): string {
  const pair = formatResourceValue(used, total)
  if (pair === missingValue) return pair
  const percent = ratioPercent(used, total)
  return percent === null ? pair : `${pair} · ${formatPercent(percent)}`
}

function formatResourceValue(used: number | null | undefined, total: number | null | undefined): string {
  const safeUsed = finiteOrNull(used)
  const safeTotal = finiteOrNull(total)
  if (safeUsed === null && safeTotal === null) return missingValue
  return `${safeUsed === null ? '--' : formatBytes(safeUsed)} / ${safeTotal === null ? '--' : formatBytes(safeTotal)}`
}

function ratioPercent(used: number | null | undefined, total: number | null | undefined): number | null {
  const safeUsed = finiteOrNull(used)
  const safeTotal = finiteOrNull(total)
  if (safeUsed === null || safeTotal === null || safeTotal <= 0) return null
  return Math.max(0, Math.min(100, (safeUsed / safeTotal) * 100))
}

