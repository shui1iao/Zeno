import { useMemo, useState } from 'react'
import type { StatePoint } from '../types'
import { formatBps, formatBytes, formatPercent, missingValue } from '../lib/format'
import { availableHistoryRanges } from '../lib/historyRange'
import { lastFiniteIndex, maxFinite, type ResourceScale } from '../lib/resourceChart'
import { HistoryRangeSelector } from './HistoryRangeSelector'
import { SmoothingSelector, type SmoothingMode } from './SmoothingSelector'
import { smoothStatePoints } from '../lib/smoothStatePoints'
import { latencyTargetCorners } from '../lib/targetGridCorners'
import { ResourceChart, type ResourceChartExtraRow, type ResourceChartSeries } from './ResourceChart'

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
}

// The metric tiles reuse the latency target grid; six fit one desktop row.
const DESKTOP_METRIC_COLUMNS = 6
const MOBILE_METRIC_COLUMNS = 3

export function StateHistoryPanel({ points, range, loading = false, error, canUseExtendedRanges = false, onRangeChange = () => {} }: StateHistoryPanelProps) {
  const rangeOptions = availableHistoryRanges(canUseExtendedRanges)
  const sorted = useMemo(() => sortStatePoints(points), [points])
  const timestamps = useMemo(() => sorted.map((point) => Date.parse(point.ts)), [sorted])
  const metrics = useMemo(() => buildResourceMetrics(sorted), [sorted])
  // Unlike the latency chart, the resource chart opens smoothed. The tiles keep
  // the real latest values; only the drawn lines (and their tooltips) change.
  const [smoothing, setSmoothing] = useState<SmoothingMode>('smooth')
  const chartMetrics = useMemo(
    () => (smoothing === 'smooth' ? buildResourceMetrics(smoothStatePoints(sorted)) : metrics),
    [smoothing, sorted, metrics],
  )
  // Like the latency targets: nothing selected means every metric is drawn.
  const [selectedKeys, setSelectedKeys] = useState<string[]>([])
  const view = useMemo(() => buildChartView(chartMetrics, selectedKeys), [chartMetrics, selectedKeys])
  const toggleMetric = (key: string) => setSelectedKeys((current) => (current.includes(key) ? current.filter((item) => item !== key) : [...current, key]))
  const sampleCount = sorted.length
  const total = metrics.length

  return (
    <section className="monitor-panel resource-history-panel" aria-label="agent state history">
      <header className="resource-history-header">
        <div className="monitor-title-row">
          <h3>系统资源趋势</h3>
          <SmoothingSelector value={smoothing} onChange={setSmoothing} ariaLabel="资源曲线平滑" />
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
        <>
          <div className="latency-target-grid resource-metric-grid" role="group" aria-label="resource metrics">
            {metrics.map((metric, index) => {
              const active = selectedKeys.includes(metric.key)
              return (
                <button
                  key={metric.key}
                  type="button"
                  aria-pressed={active}
                  data-active={active}
                  data-metric={metric.key}
                  data-desktop-corners={latencyTargetCorners(index, total, DESKTOP_METRIC_COLUMNS)}
                  data-mobile-corners={latencyTargetCorners(index, total, MOBILE_METRIC_COLUMNS)}
                  data-desktop-full-row-end={(index + 1) % DESKTOP_METRIC_COLUMNS === 0}
                  data-desktop-last-row={index >= Math.floor((total - 1) / DESKTOP_METRIC_COLUMNS) * DESKTOP_METRIC_COLUMNS}
                  data-mobile-full-row-end={(index + 1) % MOBILE_METRIC_COLUMNS === 0}
                  data-mobile-last-row={index >= Math.floor((total - 1) / MOBILE_METRIC_COLUMNS) * MOBILE_METRIC_COLUMNS}
                  title={[metric.title, metric.value, metric.detail].filter(Boolean).join(' · ')}
                  onClick={() => toggleMetric(metric.key)}
                >
                  <span className="latency-target-name">
                    <i className="latency-target-color" style={{ backgroundColor: metric.series[0]?.color }} aria-hidden="true" />
                    <span>{metric.title}</span>
                  </span>
                  <strong>{metric.value}</strong>
                  {metric.detail && <em>{metric.detail}</em>}
                </button>
              )
            })}
          </div>
          <ResourceChart
            key={`${view.key}:${smoothing}`}
            label={view.label}
            scale={view.scale}
            series={view.series}
            extraRows={view.extraRows}
            timestamps={timestamps}
          />
        </>
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
  const valid = points.filter((point) => Number.isFinite(Date.parse(point.ts)))
  for (let index = 1; index < valid.length; index += 1) {
    if (Date.parse(valid[index].ts) < Date.parse(valid[index - 1].ts)) {
      return [...valid].sort((left, right) => Date.parse(left.ts) - Date.parse(right.ts))
    }
  }
  return valid
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
      detail: (() => { const load = latestPoint(formatLoad); return load ? `负载 ${load}` : '' })(),
    },
    {
      key: 'memory',
      title: '内存',
      scale: 'percent',
      series: memorySeries,
      value: latestOf(memory, percentAt(memory)),
      detail: latestPoint((point) => { const pair = formatResourceValue(point.memoryUsedBytes, point.memoryTotalBytes); return pair === missingValue ? null : pair }),
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
      value: `↑ ${latestOf(netOut, rateAt(netOut))}`,
      detail: `↓ ${latestOf(netIn, rateAt(netIn))}`,
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

function finiteOrNull(value: number | null | undefined): number | null {
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}
