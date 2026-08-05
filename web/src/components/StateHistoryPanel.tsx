import { type MouseEvent, type ReactNode, useMemo, useState } from 'react'
import type { StatePoint } from '../types'
import { formatBps, formatBytes, formatPercent } from '../lib/format'
import { availableHistoryRanges } from '../lib/historyRange'
import { HistoryRangeSelector } from './HistoryRangeSelector'

interface StateHistoryPanelProps {
  points: StatePoint[]
  range: string
  loading?: boolean
  error?: string
  canUseExtendedRanges?: boolean
  onRangeChange?: (range: string) => void
}

interface MetricLine {
  key: string
  label: string
  values: Array<number | null>
  tooltipValues?: string[]
  color: string
}

interface MetricConfig {
  key: string
  label: string
  value: ReactNode
  tone: 'green' | 'blue' | 'purple' | 'orange'
  unitLabel: string
  domainMax?: number
  fillArea?: boolean
  compactValue?: boolean
  lines: MetricLine[]
}

const plotWidth = 640
const plotHeight = 112
export function StateHistoryPanel({ points, range, loading = false, error, canUseExtendedRanges = false, onRangeChange = () => {} }: StateHistoryPanelProps) {
  const stateRangeOptions = availableHistoryRanges(canUseExtendedRanges)
  const chartPoints = useMemo(() => downsampleStatePoints(points, range), [points, range])
  const sampleCount = chartPoints.length
  const latestCpu = latest(chartPoints, (point) => point.cpuPercent)
  const latestMemory = latestResourceUsage(chartPoints, (point) => point.memoryUsedBytes, (point) => point.memoryTotalBytes)
  const latestDisk = latestResourceUsage(chartPoints, (point) => point.diskUsedBytes, (point) => point.diskTotalBytes)
  const latestInSpeed = latest(chartPoints, (point) => point.netInSpeedBps)
  const latestOutSpeed = latest(chartPoints, (point) => point.netOutSpeedBps)
  const latestSwap = latestResourceUsage(chartPoints, (point) => point.swapUsedBytes, (point) => point.swapTotalBytes)
  const latestProcessCount = latest(chartPoints, (point) => point.processCount)
  const latestTcpConnectionCount = latest(chartPoints, (point) => point.tcpConnectionCount)
  const latestUdpConnectionCount = latest(chartPoints, (point) => point.udpConnectionCount)
  const timestamps = chartPoints.map((point) => Date.parse(point.ts))

  const metrics: MetricConfig[] = [
    {
      key: 'cpu',
      label: 'CPU',
      value: formatPercent(latestCpu),
      tone: 'green',
      unitLabel: '%',
      domainMax: 100,
      fillArea: true,
      compactValue: true,
      lines: [{ key: 'cpu', label: 'CPU', values: chartPoints.map((point) => finiteOrNull(point.cpuPercent)), color: '#22c55e' }],
    },
    {
      key: 'memory',
      label: '内存 / Swap',
      value: <><MetricValue label="内存" value={latestMemory} /><MetricValue label="Swap" value={latestSwap} /></>,
      tone: 'blue',
      unitLabel: '%',
      domainMax: 100,
      fillArea: true,
      compactValue: true,
      lines: [
        {
          key: 'memory',
          label: '内存',
          values: chartPoints.map(memoryPercent),
          tooltipValues: chartPoints.map((point) => formatResourceUsage(point.memoryUsedBytes, point.memoryTotalBytes)),
          color: '#2563eb',
        },
        {
          key: 'swap',
          label: 'Swap',
          values: chartPoints.map(swapPercent),
          tooltipValues: chartPoints.map((point) => formatResourceUsage(point.swapUsedBytes, point.swapTotalBytes)),
          color: '#0ea5e9',
        },
      ],
    },
    {
      key: 'disk',
      label: '磁盘',
      value: latestDisk,
      tone: 'purple',
      unitLabel: '%',
      domainMax: 100,
      fillArea: true,
      lines: [{
        key: 'disk',
        label: '磁盘',
        values: chartPoints.map(diskPercent),
        tooltipValues: chartPoints.map((point) => formatResourceUsage(point.diskUsedBytes, point.diskTotalBytes)),
        color: '#9333ea',
      }],
    },
    {
      key: 'network',
      label: '网络速率',
      value: <><MetricValue label="上传" value={formatBps(latestOutSpeed)} ariaLabel={`↑${formatBps(latestOutSpeed)}`} /><MetricValue label="下载" value={formatBps(latestInSpeed)} ariaLabel={`↓${formatBps(latestInSpeed)}`} /></>,
      tone: 'orange',
      unitLabel: 'B/s',
      lines: [
        { key: 'net-out', label: '上传', values: chartPoints.map((point) => finiteOrNull(point.netOutSpeedBps)), color: '#f97316' },
        { key: 'net-in', label: '下载', values: chartPoints.map((point) => finiteOrNull(point.netInSpeedBps)), color: '#06b6d4' },
      ],
    },
    {
      key: 'processes',
      label: '进程数',
      value: latestProcessCount !== null ? Math.round(latestProcessCount) : '--',
      tone: 'purple',
      unitLabel: 'count',
      fillArea: true,
      lines: [{ key: 'processes', label: '进程', values: chartPoints.map((point) => finiteOrNull(point.processCount)), color: '#a855f7' }],
    },
    {
      key: 'connections',
      label: 'TCP / UDP',
      value: <><MetricValue label="TCP" value={latestTcpConnectionCount !== null ? String(Math.round(latestTcpConnectionCount)) : '--'} /><MetricValue label="UDP" value={latestUdpConnectionCount !== null ? String(Math.round(latestUdpConnectionCount)) : '--'} /></>,
      tone: 'orange',
      unitLabel: 'count',
      lines: [
        { key: 'tcp', label: 'TCP', values: chartPoints.map((point) => finiteOrNull(point.tcpConnectionCount)), color: '#ec4899' },
        { key: 'udp', label: 'UDP', values: chartPoints.map((point) => finiteOrNull(point.udpConnectionCount)), color: '#38bdf8' },
      ],
    },
  ]

  return (
    <section className="monitor-panel resource-history-panel" aria-label="agent state history">
      <header className="resource-history-header">
        <h3>系统资源趋势</h3>
        <HistoryRangeSelector
          ariaLabel="resource history range selector"
          options={stateRangeOptions}
          value={range}
          onChange={onRangeChange}
          className="resource-range-row"
        />
      </header>

      {loading && <div className="detail-state">正在读取系统资源…</div>}
      {error && <div className="detail-state is-error">系统资源读取失败：{error}</div>}
      {!loading && !error && sampleCount === 0 && <div className="detail-state">暂无系统资源趋势数据</div>}

      {!loading && !error && sampleCount > 0 && (
        <div className="resource-history-grid">
          {metrics.map((metric) => <ResourceMetricCard key={metric.key} metric={metric} timestamps={timestamps} />)}
        </div>
      )}
    </section>
  )
}

function ResourceMetricCard({ metric, timestamps }: { metric: MetricConfig; timestamps: number[] }) {
  const domain = stateYDomain(metric.lines.flatMap((line) => line.values), metric.domainMax)
  const yTicks = yAxisTicks(domain, metric.unitLabel)
  const timeTicks = stateAxisTicks(timestamps)
  const [hoverIndex, setHoverIndex] = useState<number | null>(null)
  const hoverPointCount = timestamps.length
  const hoverPercent = hoverIndex !== null && hoverPointCount > 1 ? (hoverIndex / (hoverPointCount - 1)) * 100 : 0
  const hoverTimestamp = hoverIndex !== null ? timestamps[hoverIndex] : null

  const updateHover = (event: MouseEvent<HTMLDivElement>) => {
    if (hoverPointCount === 0) return
    const rect = event.currentTarget.getBoundingClientRect()
    const ratio = rect.width > 0 ? clamp((event.clientX - rect.left) / rect.width, 0, 1) : 0
    setHoverIndex(Math.round(ratio * Math.max(hoverPointCount - 1, 0)))
  }

  return (
    <article className={`resource-card tone-${metric.tone}`}>
      <header className="resource-card-header">
        <div className="resource-card-title-block">
          <p className="resource-card-title">{metric.label}</p>
          {metric.lines.length > 1 && (
            <span className="resource-chart-legend">
              {metric.lines.map((line) => <em key={line.key} style={{ color: line.color }}>{line.label}</em>)}
            </span>
          )}
        </div>
        <strong className={`resource-card-value${metric.compactValue ? ' is-compact' : ''}`}>{metric.value}</strong>
      </header>

      <div className="resource-chart-frame">
        <div className="resource-y-axis" aria-hidden="true">
          {yTicks.map((tick) => <span key={tick}>{tick}</span>)}
        </div>
        <div className="resource-chart-plot" onMouseMove={updateHover} onMouseLeave={() => setHoverIndex(null)}>
          <svg className="resource-chart-svg" viewBox={`0 0 ${plotWidth} ${plotHeight}`} preserveAspectRatio="none" role="img" aria-label={`${metric.label} history`}>
            {[0, 0.5, 1].map((ratio) => (
              <line key={ratio} x1={0} x2={plotWidth} y1={ratio * plotHeight} y2={ratio * plotHeight} className="resource-grid-line" vectorEffect="non-scaling-stroke" />
            ))}
            {metric.fillArea && metric.lines.map((line) => (
              <path
                key={`${line.key}-area`}
                d={chartAreaPath(line.values, domain)}
                className="resource-chart-area"
                data-series={`${line.key}-area`}
                style={{ fill: line.color }}
              />
            ))}
            {metric.lines.map((line) => (
              <path
                key={line.key}
                d={chartLinePath(line.values, domain)}
                className="resource-chart-line"
                data-series={line.key}
                style={{ stroke: line.color }}
                vectorEffect="non-scaling-stroke"
              />
            ))}
          </svg>
          {hoverIndex !== null && hoverTimestamp !== null && (
            <>
              <span className="resource-chart-hover-line" style={{ left: `${hoverPercent}%` }} aria-hidden="true" />
              <div className={`resource-chart-tooltip${hoverPercent < 24 ? ' is-left-edge' : ''}${hoverPercent > 76 ? ' is-right-edge' : ''}`} style={{ left: `${hoverPercent}%` }}>
                <time>{formatTooltipTime(hoverTimestamp)}</time>
                {metric.lines.map((line) => (
                  <span key={line.key}>
                    <i style={{ backgroundColor: line.color }} />
                    <b>{line.label}</b>
                    <strong>{line.tooltipValues?.[hoverIndex] ?? formatTooltipValue(line.values[hoverIndex], metric.unitLabel)}</strong>
                  </span>
                ))}
              </div>
            </>
          )}
        </div>
        <div className="resource-x-spacer" aria-hidden="true" />
        <div className="resource-x-axis" aria-hidden="true">
          {timeTicks.map((tick) => <span key={tick.index}>{formatStateAxisTime(tick.timestamp, timestamps)}</span>)}
        </div>
      </div>
    </article>
  )
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

function MetricValue({ label, value, ariaLabel }: { label: string; value: string; ariaLabel?: string }) {
  return <span aria-label={ariaLabel ?? `${label} ${value}`}><span className="metric-inline-label">{label}</span> {value}</span>
}

const stateNumericKeys = [
  'cpuPercent',
  'load1',
  'load5',
  'load15',
  'memoryUsedBytes',
  'memoryTotalBytes',
  'swapUsedBytes',
  'swapTotalBytes',
  'diskUsedBytes',
  'diskTotalBytes',
  'netInTotalBytes',
  'netOutTotalBytes',
  'netInSpeedBps',
  'netOutSpeedBps',
  'processCount',
  'tcpConnectionCount',
  'udpConnectionCount',
  'uptimeSeconds',
] as const satisfies ReadonlyArray<keyof Omit<StatePoint, 'ts'>>

type StateNumericKey = typeof stateNumericKeys[number]

function downsampleStatePoints(points: StatePoint[], range: string): StatePoint[] {
  const stepMs = stateRangeStepMs(range)
  if (!stepMs || points.length <= 1) return points

  const buckets = new Map<number, { sums: Record<StateNumericKey, number>; counts: Record<StateNumericKey, number> }>()
  for (const point of points) {
    const timestamp = Date.parse(point.ts)
    if (!Number.isFinite(timestamp)) continue
    const bucketTs = Math.floor(timestamp / stepMs) * stepMs
    let bucket = buckets.get(bucketTs)
    if (!bucket) {
      bucket = { sums: emptyStateRecord(), counts: emptyStateRecord() }
      buckets.set(bucketTs, bucket)
    }
    for (const key of stateNumericKeys) {
      const value = point[key]
      if (typeof value === 'number' && Number.isFinite(value)) {
        bucket.sums[key] += value
        bucket.counts[key] += 1
      }
    }
  }

  return [...buckets.entries()]
    .sort(([left], [right]) => left - right)
    .map(([timestamp, bucket]) => {
      const point = { ts: new Date(timestamp).toISOString() } as StatePoint
      for (const key of stateNumericKeys) {
        point[key] = bucket.counts[key] > 0 ? bucket.sums[key] / bucket.counts[key] : null
      }
      return point
    })
}

function emptyStateRecord(): Record<StateNumericKey, number> {
  return Object.fromEntries(stateNumericKeys.map((key) => [key, 0])) as Record<StateNumericKey, number>
}

function stateRangeStepMs(range: string): number | null {
  switch (range) {
    case '1d':
      return 30_000
    case '7d':
      return 30 * 60_000
    case '30d':
      return 2 * 60 * 60_000
    default:
      return null
  }
}

function latest(points: StatePoint[], read: (point: StatePoint) => number | null | undefined): number | null {
  for (let index = points.length - 1; index >= 0; index -= 1) {
    const value = finiteOrNull(read(points[index]))
    if (value !== null) return value
  }
  return null
}

function latestResourceUsage(
  points: StatePoint[],
  readUsed: (point: StatePoint) => number | null | undefined,
  readTotal: (point: StatePoint) => number | null | undefined,
): string {
  for (let index = points.length - 1; index >= 0; index -= 1) {
    const used = finiteOrNull(readUsed(points[index]))
    const total = finiteOrNull(readTotal(points[index]))
    if (used !== null || total !== null) return formatResourceUsage(used, total)
  }
  return 'No data'
}

export function formatResourceUsage(used: number | null | undefined, total: number | null | undefined): string {
  const safeUsed = finiteOrNull(used)
  const safeTotal = finiteOrNull(total)
  if (safeUsed === null && safeTotal === null) return 'No data'
  const pair = `${safeUsed === null ? '--' : formatBytes(safeUsed)} / ${safeTotal === null ? '--' : formatBytes(safeTotal)}`
  const percent = ratioPercent(safeUsed, safeTotal)
  return percent === null ? pair : `${pair} · ${formatPercent(percent)}`
}

function memoryPercent(point: StatePoint): number | null {
  return ratioPercent(point.memoryUsedBytes, point.memoryTotalBytes)
}

function swapPercent(point: StatePoint): number | null {
  return ratioPercent(point.swapUsedBytes, point.swapTotalBytes)
}

function diskPercent(point: StatePoint): number | null {
  return ratioPercent(point.diskUsedBytes, point.diskTotalBytes)
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

export function stateYDomain(values: Array<number | null>, forcedMax?: number): { min: number; max: number } {
  let max = Number.NEGATIVE_INFINITY
  for (const value of values) {
    if (typeof value === 'number' && Number.isFinite(value) && value > max) max = value
  }
  if (!Number.isFinite(max)) return { min: 0, max: forcedMax ?? 1 }
  return { min: 0, max: Math.max(1, forcedMax ?? Math.ceil(max * 1.15)) }
}

function chartLinePath(values: Array<number | null>, domain: { min: number; max: number }): string {
  const span = domain.max - domain.min || 1
  let open = false

  return values
    .map((value, index) => {
      if (value === null) {
        open = false
        return ''
      }
      const x = chartX(index, values.length)
      const y = plotHeight - ((value - domain.min) / span) * plotHeight
      const command = open ? 'L' : 'M'
      open = true
      return `${command} ${x.toFixed(1)} ${y.toFixed(1)}`
    })
    .filter(Boolean)
    .join(' ')
}

function chartAreaPath(values: Array<number | null>, domain: { min: number; max: number }): string {
  const span = domain.max - domain.min || 1
  let segment: Array<{ x: number; y: number }> = []
  const segments: Array<Array<{ x: number; y: number }>> = []

  values.forEach((value, index) => {
    if (value === null) {
      if (segment.length > 0) segments.push(segment)
      segment = []
      return
    }
    segment.push({
      x: chartX(index, values.length),
      y: plotHeight - ((value - domain.min) / span) * plotHeight,
    })
  })
  if (segment.length > 0) segments.push(segment)

  return segments
    .map((coords) => {
      const first = coords[0]
      const last = coords.at(-1)!
      return [
        `M ${first.x.toFixed(1)} ${plotHeight}`,
        ...coords.map((coord) => `L ${coord.x.toFixed(1)} ${coord.y.toFixed(1)}`),
        `L ${last.x.toFixed(1)} ${plotHeight}`,
        'Z',
      ].join(' ')
    })
    .join(' ')
}

function chartX(index: number, count: number): number {
  if (count <= 1) return 0
  return (index / (count - 1)) * plotWidth
}

function yAxisTicks(domain: { min: number; max: number }, unitLabel: string): string[] {
  return [domain.max, domain.min + (domain.max - domain.min) / 2, domain.min].map((value) => formatAxisValue(value, unitLabel))
}

interface StateAxisTick {
  timestamp: number
  index: number
}

function stateAxisTicks(timestamps: number[]): StateAxisTick[] {
  const valid = timestamps
    .map((timestamp, index) => ({ timestamp, index }))
    .filter((item) => Number.isFinite(item.timestamp))
  if (valid.length === 0) return []
  if (valid.length === 1) return [valid[0]]
  const middle = valid[Math.floor((valid.length - 1) / 2)]
  return [valid[0], middle, valid.at(-1)!].filter((tick, index, ticks) => ticks.findIndex((item) => item.index === tick.index) === index)
}

function formatStateAxisTime(timestamp: number, timestamps: number[]): string {
  if (!Number.isFinite(timestamp)) return '--'
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return '--'
  const valid = timestamps.filter(Number.isFinite)
  const start = valid[0] ?? timestamp
  const end = valid.at(-1) ?? timestamp
  const spanHours = (end - start) / 3_600_000
  const time = date.toLocaleTimeString('zh-CN', { hour12: false, hour: '2-digit', minute: '2-digit' })
  if (spanHours > 12) {
    const day = date.toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' })
    return `${day} ${time}`
  }
  return time
}

function formatAxisValue(value: number, unitLabel: string): string {
  if (unitLabel === '%') return `${Math.round(value)}%`
  if (unitLabel === 'count') return `${Math.round(value)}`
  return value >= 1024 ? formatBps(value).replace('/s', '') : `${Math.round(value)} ${unitLabel}`
}

function formatTooltipValue(value: number | null | undefined, unitLabel: string): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '--'
  if (unitLabel === '%') return `${value.toFixed(1)}%`
  if (unitLabel === 'count') return `${Math.round(value)}`
  return formatBps(value)
}

function formatTooltipTime(timestamp: number): string {
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return '--'
  return date.toLocaleString('zh-CN', { hour12: false, month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })
}
