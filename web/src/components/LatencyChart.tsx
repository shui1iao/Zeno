import { useEffect, useId, useMemo, useState, type MouseEvent as ReactMouseEvent } from 'react'
import type { LatencyPoint } from '../types'
import {
  applyKulinPeakCut,
  buildKulinChartRows,
  buildKulinTargetSeries,
  selectKulinChartView,
  type KulinChartRow,
  type KulinTargetSeries,
} from '../lib/kulinLatencyChart'
import { formatLatency, formatPercent, formatTooltipDateTime } from '../lib/format'
import { finiteOrNull } from '../lib/number'
import { formatAxisTime as formatSpanAxisTime, svgPointerX } from '../lib/resourceChart'
import { latencySeriesColor as paletteColor } from '../lib/seriesColors'
import { OverlaySurface } from './OverlaySurface'

interface LatencyChartProps {
  points: LatencyPoint[]
  eyebrow?: string
  title?: string
  compactHeader?: boolean
  hideHeader?: boolean
  hideLegend?: boolean
  peakCut?: boolean
  activeTargetIds?: string[]
  /** Temporarily emphasised series while its target tile is hovered. */
  highlightTargetId?: string | null
  /** Colour per series id in the global (admin) order; falls back to the local index. */
  seriesColors?: ReadonlyMap<string, string>
}

const desktopLayout = { width: 960, height: 372, lineStrokeWidth: 1, pad: { left: 60, right: 24, top: 24, bottom: 58 } }
const mobileLayout = { width: 400, height: 332, lineStrokeWidth: 1, pad: { left: 50, right: 16, top: 22, bottom: 58 } }
/** Gap between the plot and the packet-loss lane, and the lane height. */
const lossLaneOffset = 15
const lossLaneHeight = 5
const packetLossColor = '#94a3b8'
const maxDrawableLatencyMs = 5000

export function latencySeriesColor(index: number): string {
  return paletteColor(index)
}

function seriesColorResolver(series: KulinTargetSeries[], seriesColors?: ReadonlyMap<string, string>) {
  const indexByKey = new Map<string, number>()
  series.forEach((item, index) => {
    if (!indexByKey.has(item.targetId)) indexByKey.set(item.targetId, index)
  })
  return (key: string): string => seriesColors?.get(key) ?? latencySeriesColor(indexByKey.get(key) ?? 0)
}

export function LatencyChart({
  points,
  eyebrow = 'Latency',
  title = '多目标延迟图',
  compactHeader = false,
  hideHeader = false,
  hideLegend = false,
  peakCut = false,
  activeTargetIds = [],
  highlightTargetId = null,
  seriesColors,
}: LatencyChartProps) {
  const { width, height, lineStrokeWidth, pad } = useLatencyChartLayout()
  const reactClipId = useId()
  const clipId = `latency-plot-${reactClipId.replace(/:/g, '')}`
  const activeTargetKey = JSON.stringify(activeTargetIds)
  const series = useMemo(() => buildKulinTargetSeries(points), [points])
  const colorFor = useMemo(() => seriesColorResolver(series, seriesColors), [series, seriesColors])
  const allRows = useMemo(() => buildKulinChartRows(series), [series])
  const baseView = useMemo(() => selectKulinChartView(series, allRows, activeTargetIds), [series, allRows, activeTargetKey])
  const rows = useMemo(() => (peakCut ? applyKulinPeakCut(baseView.rows, baseView.lineKeys) : baseView.rows), [baseView, peakCut])
  const timestamps = useMemo(() => rows.map((row) => row.created_at), [rows])
  const timeStart = timestamps[0] ?? 0
  const timeEnd = timestamps.at(-1) ?? timeStart
  const timeSpan = Math.max(0, timeEnd - timeStart)
  const maxAxisTicks = width <= 480 ? 4 : 14
  const axisLabelCharWidth = width <= 480 ? 6 : 7.2
  const candidateAxisTicks = useMemo(
    () => axisTicksForTimestamps(timestamps, maxAxisTicks),
    [timestamps, maxAxisTicks],
  )
  const plotHeight = height - pad.top - pad.bottom
  const domain = useMemo(() => integerYAxisDomain(yDomainForRows(rows, baseView.lineKeys)), [rows, baseView.lineKeys])
  const packetLossSeries = baseView.showPacketLossArea
    ? series.find((item) => item.targetId === activeTargetIds[0])
    : undefined
  const lossRows = baseView.showPacketLossArea ? rows : []
  const visibleLineKeys = baseView.lineKeys
  const hoverColumns = useMemo(() => hoverColumnsForRows(rows, visibleLineKeys, series), [rows, visibleLineKeys, series])
  const legendSeries = useMemo(() => (activeTargetIds.length > 0
    ? series.filter((item) => activeTargetIds.includes(item.targetId))
    : series), [series, activeTargetKey])
  const [hoverColumn, setHoverColumn] = useState<HoverColumn | null>(null)

  const plotWidth = width - pad.left - pad.right
  const x = useMemo(() => (createdAt: number) => {
    if (timeSpan <= 0) return pad.left
    return pad.left + ((createdAt - timeStart) / timeSpan) * plotWidth
  }, [pad.left, plotWidth, timeSpan, timeStart])
  // The nominal first tick can be one sample cadence before the oldest row
  // (1440 one-minute samples contain 1439 intervals). Give axis labels their own
  // domain so that nominal tick lands exactly on the left edge and every fixed-
  // step tick occupies the same number of pixels. The data line keeps its actual
  // sample domain; the difference is only one cadence at the left boundary.
  // Only that nominal tick (before the oldest row) may move the axis start: when
  // the first tick falls inside the data, labels must share the line's domain or
  // every label would shift by up to one step.
  const firstAxisTick = candidateAxisTicks[0]
  const axisTimeStart = firstAxisTick !== undefined && firstAxisTick < timeStart ? firstAxisTick : timeStart
  const axisTimeSpan = Math.max(0, timeEnd - axisTimeStart)
  const axisX = useMemo(() => (createdAt: number) => {
    if (axisTimeSpan <= 0) return pad.left
    return pad.left + ((createdAt - axisTimeStart) / axisTimeSpan) * plotWidth
  }, [axisTimeSpan, axisTimeStart, pad.left, plotWidth])
  const yDelay = useMemo(
    () => (value: number) => pad.top + (1 - (value - domain.min) / (domain.max - domain.min)) * plotHeight,
    [domain.max, domain.min, pad.top, plotHeight],
  )
  const yLoss = useMemo(
    () => (value: number) => pad.top + (1 - Math.max(0, Math.min(100, value)) / 100) * plotHeight,
    [pad.top, plotHeight],
  )

  // Tick selection above is time-based, so it cannot know how wide the rendered
  // labels are. The newest sample is "now" and is force-added as the final tick,
  // which lands an arbitrary distance from the previous hour mark -- as little as
  // a few pixels. Drop labels that would physically collide, keeping the last
  // one: the right edge is the reading anchor, so a stale-looking axis end is
  // worse than one missing intermediate mark.
  const axisTicks = useMemo(() => pruneCollidingAxisTicks(
    candidateAxisTicks,
    (tick) => clampAxisTickX(axisX(tick), width, pad),
    (tick) => formatAxisTime(tick, timestamps),
    axisLabelCharWidth,
    (tick) => axisTickAnchor(axisX(tick), width, pad),
  ), [axisLabelCharWidth, axisX, candidateAxisTicks, pad, timestamps, width])

  const lossAreaD = useMemo(
    () => (lossRows.length > 0 && baseView.packetLossKey ? packetLossAreaPath(lossRows, baseView.packetLossKey, x, yLoss) : ''),
    [baseView.packetLossKey, lossRows, x, yLoss],
  )
  const linePathsByKey = useMemo(
    () => new Map(visibleLineKeys.map((key) => [key, linePath(rows, key, x, yDelay)])),
    [rows, visibleLineKeys, x, yDelay],
  )
  // Focused views keep the other targets as faint context on the focused scale.
  const contextPaths = useMemo(() => {
    if (activeTargetIds.length === 0) return []
    const visible = new Set(visibleLineKeys)
    return series
      .filter((item) => !visible.has(item.targetId))
      .map((item) => ({ key: item.targetId, d: linePath(allRows, item.targetId, x, yDelay) }))
  }, [activeTargetKey, allRows, series, visibleLineKeys, x, yDelay])
  const lossLane = useMemo(
    () => lossLaneBins(series, activeTargetIds.length > 0 ? visibleLineKeys : series.map((item) => item.targetId), timeStart, timeSpan, Math.max(12, Math.floor(plotWidth / 6))),
    [activeTargetKey, plotWidth, series, timeSpan, timeStart, visibleLineKeys],
  )
  const lossLaneY = pad.top + plotHeight + lossLaneOffset
  const emphasisKey = activeTargetIds.length === 0 && highlightTargetId && visibleLineKeys.includes(highlightTargetId) ? highlightTargetId : null
  const orderedLineKeys = emphasisKey ? [...visibleLineKeys.filter((key) => key !== emphasisKey), emphasisKey] : visibleLineKeys

  const setActiveHoverColumn = (column: HoverColumn | null) => {
    setHoverColumn((current) => (current?.createdAt === column?.createdAt ? current : column))
  }

  const hoverColumnForSvgX = (svgX: number): HoverColumn | null => {
    if (hoverColumns.length === 0 || timestamps.length === 0) return null
    const plotWidth = width - pad.left - pad.right
    const ratio = plotWidth > 0 ? Math.max(0, Math.min(1, (svgX - pad.left) / plotWidth)) : 0
    const targetTimestamp = timeStart + ratio * timeSpan
    let low = 0
    let high = hoverColumns.length - 1
    while (low < high) {
      const middle = Math.floor((low + high) / 2)
      if (hoverColumns[middle].createdAt < targetTimestamp) low = middle + 1
      else high = middle
    }
    const right = hoverColumns[low]
    const left = hoverColumns[Math.max(0, low - 1)]
    if (!right) return left ?? null
    if (!left) return right
    return Math.abs(right.createdAt - targetTimestamp) < Math.abs(targetTimestamp - left.createdAt) ? right : left
  }

  const handleHoverMove = (event: ReactMouseEvent<SVGRectElement>) => {
    const svgX = svgPointerX(event.currentTarget, event.clientX, event.clientY)
    if (svgX === null) return
    setActiveHoverColumn(hoverColumnForSvgX(svgX))
  }

  return (
    <section className={`latency-panel${compactHeader ? ' is-compact' : ''}`}>
      {!hideHeader && (
        <div className="latency-panel__header">
          <div>
            <p className="eyebrow">{eyebrow}</p>
            <h2>{title}</h2>
          </div>
          {!compactHeader && (
            <div className="range-tabs" aria-label="range selector">
              <button className="is-active">1h</button>
              <button>6h</button>
              <button>24h</button>
              <button>7d</button>
            </div>
          )}
        </div>
      )}

      <svg className="latency-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label="latency chart" onMouseLeave={() => setHoverColumn(null)}>
        <defs>
          <clipPath id={clipId}>
            <rect x={pad.left} y={pad.top} width={width - pad.left - pad.right} height={plotHeight} />
          </clipPath>
        </defs>
        {[0, 0.25, 0.5, 0.75, 1].map((ratio) => {
          const yy = pad.top + ratio * plotHeight
          const value = domain.max - ratio * (domain.max - domain.min)
          return (
            <g key={ratio}>
              <line x1={pad.left} x2={width - pad.right} y1={yy} y2={yy} className="grid-line" />
              {/* Right-aligned to the plot edge: the spaced " ms" label must not run into the plot. */}
              <text x={pad.left - 6} y={yy + 4} className="axis-label" textAnchor="end">{formatYAxisValue(value)} ms</text>
            </g>
          )
        })}
        {axisTicks.map((tick) => {
          const xx = axisX(tick)
          return (
            <text
              key={tick}
              x={clampAxisTickX(xx, width, pad)}
              y={height - 12}
              className="axis-label"
              textAnchor={axisTickAnchor(xx, width, pad)}
            >
              {formatAxisTime(tick, timestamps)}
            </text>
          )
        })}

        {lossAreaD !== '' && (
          <path
            className="packet-loss-area"
            d={lossAreaD}
            fill={packetLossColor}
            fillOpacity={0.18}
            stroke="none"
            clipPath={`url(#${clipId})`}
          />
        )}

        {contextPaths.map((item) => (
          <path
            key={`context-${item.key}`}
            className="latency-context-line"
            d={item.d}
            fill="none"
            stroke={colorFor(item.key)}
            strokeOpacity={0.1}
            strokeWidth={lineStrokeWidth}
            vectorEffect="non-scaling-stroke"
            clipPath={`url(#${clipId})`}
          />
        ))}

        {orderedLineKeys.map((key) => (
          <path
            key={key}
            className={emphasisKey === key ? 'latency-line is-emphasis' : 'latency-line'}
            d={linePathsByKey.get(key) ?? ''}
            fill="none"
            stroke={colorFor(key)}
            strokeOpacity={emphasisKey && emphasisKey !== key ? 0.22 : 1}
            strokeWidth={emphasisKey === key || activeTargetIds.length === 1 ? lineStrokeWidth * 1.6 : lineStrokeWidth}
            vectorEffect="non-scaling-stroke"
            clipPath={`url(#${clipId})`}
          />
        ))}

        {lossLane.length > 0 && (
          <g className="latency-loss-lane" aria-label="丢包轨道">
            <text x={pad.left - 6} y={lossLaneY + lossLaneHeight} className="axis-label latency-loss-lane__label" textAnchor="end">丢包</text>
            {lossLane.map((bin) => {
              const x0 = pad.left + bin.start * plotWidth
              const x1 = pad.left + bin.end * plotWidth
              return (
                <rect key={bin.start} className={`latency-loss-bin is-${bin.tone}`} x={x0} y={lossLaneY} width={Math.max(1, x1 - x0 - 1)} height={lossLaneHeight} rx={1} />
              )
            })}
          </g>
        )}

        {hoverColumn && (
          <g className="latency-hover-column is-active">
            <line
              className="latency-hover-guide"
              x1={x(hoverColumn.createdAt)}
              x2={x(hoverColumn.createdAt)}
              y1={pad.top}
              y2={height - pad.bottom}
              vectorEffect="non-scaling-stroke"
            />
            {hoverColumn.points.map((point) => (
              <circle
                key={`${point.key}-${hoverColumn.createdAt}`}
                className="latency-hover-point"
                cx={x(hoverColumn.createdAt)}
                cy={yDelay(point.delay)}
                r={5}
                fill={colorFor(point.key)}
                clipPath={`url(#${clipId})`}
              />
            ))}
          </g>
        )}
        <rect
          className="latency-hover-hit"
          x={pad.left}
          y={pad.top}
          width={width - pad.left - pad.right}
          height={plotHeight}
          aria-hidden="true"
          aria-label={hoverColumn ? hoverColumnTitle(hoverColumn) : '延迟图表悬浮区域'}
          onMouseEnter={handleHoverMove}
          onMouseMove={handleHoverMove}
          onClick={handleHoverMove}
        />
        {hoverColumn && (
          <LatencyTooltip
            column={hoverColumn}
            colorFor={colorFor}
            x={x(hoverColumn.createdAt)}
            layout={{ width, height, lineStrokeWidth, pad }}
          />
        )}
      </svg>

      {!hideLegend && (
        <div className="latency-legend">
          {legendSeries.map((item) => (
            <span key={item.targetId}><i style={{ background: colorFor(item.targetId) }} />{item.targetName}</span>
          ))}
          {baseView.showPacketLossArea && baseView.packetLossKey && packetLossSeries && (
            <span><i style={{ background: packetLossColor }} />{packetLossSeries.targetName} 丢包 {formatPercent(avgPacketLoss(lossRows, baseView.packetLossKey))}</span>
          )}
        </div>
      )}
    </section>
  )
}

export type LossLaneTone = 'none' | 'light' | 'warning' | 'danger' | 'empty'

export interface LossLaneBin {
  start: number
  end: number
  tone: LossLaneTone
}

/**
 * Average packet loss of the visible targets per time bucket, so loss is readable
 * under the latency lines. Buckets without any sample are "no data", which is
 * distinct from a measured 0%.
 */
export function lossLaneBins(series: KulinTargetSeries[], keys: string[], timeStart: number, timeSpan: number, binCount: number): LossLaneBin[] {
  if (timeSpan < 0 || binCount <= 0 || keys.length === 0) return []
  const wanted = new Set(keys)
  if (timeSpan === 0) {
    let sum = 0
    let count = 0
    series.forEach((item) => {
      if (!wanted.has(item.targetId)) return
      item.points.forEach((point) => {
        if (point.packet_loss === null || !Number.isFinite(point.packet_loss)) return
        if (point.created_at !== timeStart) return
        sum += point.packet_loss
        count += 1
      })
    })
    if (count === 0) return []
    const average = sum / count
    const tone: LossLaneTone = average >= 5 ? 'danger' : average >= 1 ? 'warning' : average > 0 ? 'light' : 'none'
    return [{ start: 0, end: 1 / binCount, tone }]
  }
  const sums = new Array<number>(binCount).fill(0)
  const counts = new Array<number>(binCount).fill(0)
  series.forEach((item) => {
    if (!wanted.has(item.targetId)) return
    item.points.forEach((point) => {
      if (point.packet_loss === null || !Number.isFinite(point.packet_loss)) return
      const ratio = (point.created_at - timeStart) / timeSpan
      if (ratio < 0 || ratio > 1) return
      const bin = Math.min(binCount - 1, Math.floor(ratio * binCount))
      sums[bin] += point.packet_loss
      counts[bin] += 1
    })
  })
  return sums.map((sum, bin) => {
    const start = bin / binCount
    const end = (bin + 1) / binCount
    if (counts[bin] === 0) return { start, end, tone: 'empty' as const }
    const average = sum / counts[bin]
    const tone: LossLaneTone = average >= 5 ? 'danger' : average >= 1 ? 'warning' : average > 0 ? 'light' : 'none'
    return { start, end, tone }
  })
}

function useLatencyChartLayout() {
  const [isMobile, setIsMobile] = useState(() => typeof window !== 'undefined' && window.matchMedia('(max-width: 767px)').matches)

  useEffect(() => {
    if (typeof window === 'undefined') return undefined
    const query = window.matchMedia('(max-width: 767px)')
    const update = () => setIsMobile(query.matches)
    update()
    query.addEventListener('change', update)
    return () => query.removeEventListener('change', update)
  }, [])

  return isMobile ? mobileLayout : desktopLayout
}

function LatencyTooltip({ column, colorFor, x: tooltipAnchorX, layout }: { column: HoverColumn; colorFor: (key: string) => string; x: number; layout: typeof desktopLayout }) {
  const { width, height, pad } = layout
  const isMobile = width <= 480
  const plotWidth = width - pad.left - pad.right
  const plotHeight = height - pad.top - pad.bottom
  const tooltipColumns = column.points.length > (isMobile ? 4 : 6) ? 2 : 1
  const preferredWidth = tooltipColumns === 2 ? 390 : 270
  const tooltipWidth = Math.min(preferredWidth, plotWidth)
  const tooltipViewportHeight = plotHeight - 8
  const tooltipX = Math.max(pad.left, Math.min(width - pad.right - tooltipWidth, tooltipAnchorX + 12))

  return (
    <g className="latency-chart-tooltip" transform={`translate(${tooltipX} ${pad.top + 4})`}>
      <foreignObject width={tooltipWidth} height={tooltipViewportHeight}>
        <div className="latency-tooltip-viewport">
          <OverlaySurface className="latency-tooltip-card">
            <time>{formatTooltipTime(column.createdAt)}</time>
            <div className="latency-tooltip-grid" style={{ gridTemplateColumns: `repeat(${tooltipColumns}, minmax(0, 1fr))` }}>
              {column.points.map((point) => (
                <span key={`${point.key}-${column.createdAt}`} className="latency-tooltip-row">
                  <i style={{ backgroundColor: colorFor(point.key) }} />
                  <b>{point.label}</b>
                  <strong>{formatLatencyValue(point.delay)}</strong>
                </span>
              ))}
            </div>
          </OverlaySurface>
        </div>
      </foreignObject>
    </g>
  )
}

function linePath(rows: KulinChartRow[], key: string, x: (createdAt: number) => number, y: (value: number) => number): string {
  let hasOpenSegment = false
  const parts: string[] = []
  for (let index = 0; index < rows.length; index += 1) {
    const row = rows[index]
    const value = rowNumber(row, key)
    if (value === null) {
      hasOpenSegment = false
      continue
    }
    const command = hasOpenSegment ? 'L' : 'M'
    hasOpenSegment = true
    parts.push(`${command} ${x(row.created_at).toFixed(2)} ${y(value).toFixed(2)}`)
  }
  return parts.join(' ')
}

function packetLossAreaPath(rows: KulinChartRow[], packetLossKey: string, x: (createdAt: number) => number, yLoss: (value: number) => number): string {
  const coords = rows
    .map((row) => {
      const value = rowNumber(row, packetLossKey)
      return value === null ? null : { x: x(row.created_at), y: yLoss(value) }
    })
    .filter((coord): coord is { x: number; y: number } => coord !== null)

  if (coords.length === 0) return ''
  const baseline = yLoss(0)
  return [
    `M ${coords[0].x.toFixed(2)} ${baseline.toFixed(2)}`,
    ...coords.map((coord) => `L ${coord.x.toFixed(2)} ${coord.y.toFixed(2)}`),
    `L ${coords.at(-1)!.x.toFixed(2)} ${baseline.toFixed(2)}`,
    'Z',
  ].join(' ')
}

export function yDomainForRows(rows: KulinChartRow[], keys: string[]): { min: number; max: number } {
  let min = Number.POSITIVE_INFINITY
  let max = Number.NEGATIVE_INFINITY
  for (const row of rows) {
    for (const key of keys) {
      const value = rowNumber(row, key)
      if (value === null) continue
      const cappedValue = Math.min(value, maxDrawableLatencyMs)
      if (cappedValue < min) min = cappedValue
      if (cappedValue > max) max = cappedValue
    }
  }
  if (!Number.isFinite(min) || !Number.isFinite(max)) return { min: 0, max: 1 }
  const span = max - min
  if (span <= 0) {
    const padding = Math.max(0.5, Math.abs(max) * 0.05)
    return cappedLatencyDomain(Math.max(0, min - padding), max + padding)
  }
  const padding = Math.max(span * 0.15, max * 0.002, 0.05)
  return cappedLatencyDomain(Math.max(0, min - padding), max + padding)
}

function cappedLatencyDomain(min: number, max: number): { min: number; max: number } {
  const cappedMax = Math.min(maxDrawableLatencyMs, Math.max(1, max))
  const cappedMin = Math.max(0, Math.min(min, cappedMax - 0.5))
  return { min: cappedMin, max: cappedMax }
}

export function integerYAxisDomain(domain: { min: number; max: number }): { min: number; max: number } {
  const min = Math.max(0, Math.min(maxDrawableLatencyMs, domain.min))
  const max = Math.max(min, Math.min(maxDrawableLatencyMs, domain.max))
  const tickStep = Math.max(1, Math.ceil((max - min) / 4))
  const axisSpan = tickStep * 4
  const highestStart = maxDrawableLatencyMs - axisSpan
  let axisMin = Math.floor((min + max - axisSpan) / 2)
  axisMin = Math.max(0, Math.min(highestStart, axisMin))
  if (axisMin > min) axisMin = Math.floor(min)
  if (axisMin + axisSpan < max) axisMin = Math.ceil(max - axisSpan)
  axisMin = Math.max(0, Math.min(highestStart, axisMin))
  return { min: axisMin, max: axisMin + axisSpan }
}

function formatYAxisValue(value: number): string {
  return Math.round(value).toFixed(0)
}

function rowNumber(row: KulinChartRow, key: string): number | null {
  return finiteOrNull(row[key])
}

interface HoverPoint {
  key: string
  label: string
  delay: number
}

interface HoverColumn {
  createdAt: number
  points: HoverPoint[]
}

function hoverColumnTitle(column: HoverColumn): string {
  return [
    formatTooltipTime(column.createdAt),
    ...column.points.map((point) => `${point.label} · ${formatLatencyValue(point.delay)}`),
  ].join('\n')
}

function hoverColumnsForRows(rows: KulinChartRow[], keys: string[], series: KulinTargetSeries[]): HoverColumn[] {
  const labelsByTargetId = new Map(series.map((target) => [target.targetId, target.targetName]))
  const columns: HoverColumn[] = []
  for (let rowIndex = 0; rowIndex < rows.length; rowIndex += 1) {
    const row = rows[rowIndex]
    const points: HoverPoint[] = []
    for (let keyIndex = 0; keyIndex < keys.length; keyIndex += 1) {
      const key = keys[keyIndex]
      const delay = rowNumber(row, key)
      if (delay === null) continue
      points.push({ key, label: labelsByTargetId.get(key) ?? key, delay })
    }
    if (points.length > 0) {
      columns.push({ createdAt: row.created_at, points })
    }
  }
  return columns
}

function formatLatencyValue(value: number): string {
  return formatLatency(value)
}

function formatTooltipTime(createdAt: number): string {
  return formatTooltipDateTime(createdAt)
}

function avgPacketLoss(rows: KulinChartRow[], packetLossKey: string): number {
  let total = 0
  let count = 0
  for (const row of rows) {
    const value = rowNumber(row, packetLossKey)
    if (value === null) continue
    total += value
    count += 1
  }
  return count === 0 ? 0 : total / count
}

const hourMs = 3_600_000

// Candidate spacings, in hours. A 24-hour desktop chart gets a two-hour step:
// 19:31, 17:31, 15:31 ... -- the current time is the reading anchor, not a wall-
// clock boundary.
const axisStepHours = [1, 2, 3, 4, 6, 8, 12, 24]

// Ticks walk backwards from the newest sample by one fixed step, so every gap is
// identical by construction and the rightmost label is always the actual latest
// sample.
//
// Two earlier attempts got this wrong. Selecting by wall clock (every ":00", or
// every 2nd hour) made spacing a side effect of where the window happened to
// start, because the endpoints are "now" and exactly N hours earlier and neither
// lands on an hour boundary. Spacing positions evenly and then snapping each one
// to its nearest hour fixed the overlap but reintroduced unequal gaps: two
// neighbouring marks could round in opposite directions, leaving a 2h gap beside
// a 3h gap. Aligning the fixed step to midnight still left two irregular endpoint
// gaps. The newest sample itself must therefore be the anchor.
export function axisTicksForTimestamps(timestamps: number[], maxTicks: number): number[] {
  if (timestamps.length <= 1) return timestamps
  if (timestamps.length < 6) return [timestamps[0], timestamps.at(-1)!]

  const start = timestamps[0]
  const end = timestamps.at(-1)!
  const span = end - start
  if (span <= 0) return [end]

  const stepHours = chooseAxisStepHours(span, maxTicks)
  const stepMs = stepHours * hourMs

  const ticks: number[] = []
  for (let tick = end; tick >= start; tick -= stepMs) {
    ticks.unshift(tick)
  }

  // A regular N-sample grid contains only N-1 intervals: 1440 one-minute
  // samples start at 19:32 but represent the nominal 24h range ending at 19:31.
  // Include the next backward tick only when it misses the first sample by no
  // more than one observed cadence. It will be clamped to the left plot edge.
  const previousTick = ticks[0] - stepMs
  if (start - previousTick > 0 && start - previousTick <= axisSampleCadence(timestamps)) {
    ticks.unshift(previousTick)
  }
  return ticks
}

function axisSampleCadence(timestamps: number[]): number {
  if (timestamps.length < 2) return 0
  const first = timestamps[1] - timestamps[0]
  const last = timestamps[timestamps.length - 1] - timestamps[timestamps.length - 2]
  const positive = [first, last].filter((value) => value > 0)
  return positive.length > 0 ? Math.min(...positive) : 0
}

// Picks the smallest step that keeps the tick count within budget. Falling back
// to whole days lets long windows keep a fixed step rather than degrading into
// an arbitrary spacing.
function chooseAxisStepHours(span: number, maxTicks: number): number {
  const interiorSlots = Math.max(1, maxTicks - 1)
  const idealHours = span / interiorSlots / hourMs
  for (const candidate of axisStepHours) {
    if (candidate >= idealHours) return candidate
  }
  return Math.ceil(idealHours / 24) * 24
}

function axisTickAnchor(x: number, width: number, pad: { left: number; right: number }): 'start' | 'middle' | 'end' {
  if (x <= pad.left + 8) return 'start'
  if (x >= width - pad.right - 8) return 'end'
  return 'middle'
}

export function clampAxisTickX(x: number, width: number, pad: { left: number; right: number }): number {
  return Math.min(width - pad.right, Math.max(pad.left, x))
}

// The horizontal extent a label occupies, which depends on its anchor: 'start'
// grows rightward, 'end' grows leftward, 'middle' grows both ways.
function axisLabelExtent(
  centre: number,
  text: string,
  charWidth: number,
  anchor: 'start' | 'middle' | 'end',
): { left: number; right: number } {
  const labelWidth = text.length * charWidth
  if (anchor === 'start') return { left: centre, right: centre + labelWidth }
  if (anchor === 'end') return { left: centre - labelWidth, right: centre }
  return { left: centre - labelWidth / 2, right: centre + labelWidth / 2 }
}

// Both endpoints are reserved before anything else, then interior ticks are
// filled in where they fit. The endpoints carry the axis range -- the oldest
// sample and "now" -- whereas interior marks are interchangeable round hours,
// so an interior label must always be the one sacrificed. Sweeping backwards
// keeps the marks that remain aligned to the right edge, and every candidate is
// checked against the reserved first tick as well as its right-hand neighbour.
export function pruneCollidingAxisTicks(
  ticks: number[],
  xOf: (tick: number) => number,
  labelOf: (tick: number) => string,
  charWidth: number,
  anchorOf: (tick: number) => 'start' | 'middle' | 'end',
  minGapPx = 6,
): number[] {
  if (ticks.length <= 2) return ticks

  const extentOf = (tick: number) => axisLabelExtent(xOf(tick), labelOf(tick), charWidth, anchorOf(tick))
  const firstExtent = extentOf(ticks[0])
  const lastExtent = extentOf(ticks[ticks.length - 1])

  // Nothing fits between endpoints that already collide; showing the range is
  // still more useful than showing one endpoint plus an arbitrary round hour.
  if (firstExtent.right + minGapPx > lastExtent.left) return [ticks[0], ticks[ticks.length - 1]]

  const interior: number[] = []
  let nextExtent = lastExtent

  for (let index = ticks.length - 2; index >= 1; index -= 1) {
    const extent = extentOf(ticks[index])
    if (extent.right + minGapPx > nextExtent.left) continue
    if (firstExtent.right + minGapPx > extent.left) continue
    interior.push(ticks[index])
    nextExtent = extent
  }

  return [ticks[0], ...interior.reverse(), ticks[ticks.length - 1]]
}

function formatAxisTime(createdAt: number, timestamps: number[]): string {
  const start = timestamps[0] ?? createdAt
  const end = timestamps.at(-1) ?? createdAt
  return formatSpanAxisTime(createdAt, end - start)
}
