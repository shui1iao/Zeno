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
import { formatPercent } from '../lib/format'

interface LatencyChartProps {
  points: LatencyPoint[]
  eyebrow?: string
  title?: string
  compactHeader?: boolean
  hideHeader?: boolean
  hideLegend?: boolean
  peakCut?: boolean
  activeTargetIds?: string[]
}

const desktopLayout = { width: 960, height: 360, lineStrokeWidth: 1, pad: { left: 52, right: 24, top: 24, bottom: 44 } }
const mobileLayout = { width: 400, height: 320, lineStrokeWidth: 1, pad: { left: 46, right: 16, top: 22, bottom: 44 } }
const palette = ['#22c55e', '#38bdf8', '#f59e0b', '#a78bfa', '#fb7185', '#14b8a6', '#84cc16', '#f97316', '#06b6d4', '#e879f9']
const packetLossColor = '#94a3b8'
const maxDrawableLatencyMs = 5000

export function latencySeriesColor(index: number): string {
  const normalized = ((Math.trunc(index) % palette.length) + palette.length) % palette.length
  return palette[normalized]
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
}: LatencyChartProps) {
  const { width, height, lineStrokeWidth, pad } = useLatencyChartLayout()
  const reactClipId = useId()
  const clipId = `latency-plot-${reactClipId.replace(/:/g, '')}`
  const activeTargetKey = JSON.stringify(activeTargetIds)
  const series = useMemo(() => buildKulinTargetSeries(points), [points])
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

  const x = (createdAt: number) => {
    if (timeSpan <= 0) return pad.left
    return pad.left + ((createdAt - timeStart) / timeSpan) * (width - pad.left - pad.right)
  }
  // The nominal first tick can be one sample cadence before the oldest row
  // (1440 one-minute samples contain 1439 intervals). Give axis labels their own
  // domain so that nominal tick lands exactly on the left edge and every fixed-
  // step tick occupies the same number of pixels. The data line keeps its actual
  // sample domain; the difference is only one cadence at the left boundary.
  const axisTimeStart = candidateAxisTicks[0] ?? timeStart
  const axisTimeSpan = Math.max(0, timeEnd - axisTimeStart)
  const axisX = (createdAt: number) => {
    if (axisTimeSpan <= 0) return pad.left
    return pad.left + ((createdAt - axisTimeStart) / axisTimeSpan) * (width - pad.left - pad.right)
  }
  const yDelay = (value: number) => pad.top + (1 - (value - domain.min) / (domain.max - domain.min)) * plotHeight
  const yLoss = (value: number) => pad.top + (1 - Math.max(0, Math.min(100, value)) / 100) * plotHeight

  // Tick selection above is time-based, so it cannot know how wide the rendered
  // labels are. The newest sample is "now" and is force-added as the final tick,
  // which lands an arbitrary distance from the previous hour mark -- as little as
  // a few pixels. Drop labels that would physically collide, keeping the last
  // one: the right edge is the reading anchor, so a stale-looking axis end is
  // worse than one missing intermediate mark.
  const axisTicks = pruneCollidingAxisTicks(
    candidateAxisTicks,
    (tick) => clampAxisTickX(axisX(tick), width, pad),
    (tick) => formatAxisTime(tick, timestamps),
    axisLabelCharWidth,
    (tick) => axisTickAnchor(axisX(tick), width, pad),
  )

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
    const svg = event.currentTarget.ownerSVGElement
    const ctm = svg?.getScreenCTM()
    if (!svg || !ctm) return
    const point = svg.createSVGPoint()
    point.x = event.clientX
    point.y = event.clientY
    const svgPoint = point.matrixTransform(ctm.inverse())
    setActiveHoverColumn(hoverColumnForSvgX(svgPoint.x))
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
              <text x={12} y={yy + 4} className="axis-label">{formatYAxisValue(value)}ms</text>
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

        {lossRows.length > 0 && baseView.packetLossKey && (
          <path
            className="packet-loss-area"
            d={packetLossAreaPath(lossRows, baseView.packetLossKey, x, yLoss)}
            fill={packetLossColor}
            fillOpacity={0.18}
            stroke="none"
            clipPath={`url(#${clipId})`}
          />
        )}

        {visibleLineKeys.map((key) => (
          <path
            key={key}
            d={linePath(rows, key, x, yDelay)}
            fill="none"
            stroke={latencySeriesColor(paletteIndexForKey(series, key))}
            strokeWidth={lineStrokeWidth}
            vectorEffect="non-scaling-stroke"
            clipPath={`url(#${clipId})`}
          />
        ))}

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
                fill={latencySeriesColor(paletteIndexForKey(series, point.key))}
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
          aria-label={hoverColumn?.title ?? '延迟图表悬浮区域'}
          onMouseEnter={handleHoverMove}
          onMouseMove={handleHoverMove}
        />
        {hoverColumn && (
          <LatencyTooltip
            column={hoverColumn}
            series={series}
            x={x(hoverColumn.createdAt)}
            layout={{ width, height, lineStrokeWidth, pad }}
          />
        )}
      </svg>

      {!hideLegend && (
        <div className="latency-legend">
          {legendSeries.map((item, index) => {
            const seriesIndex = series.findIndex((seriesItem) => seriesItem.targetId === item.targetId)
            return <span key={item.targetId}><i style={{ background: latencySeriesColor(seriesIndex >= 0 ? seriesIndex : index) }} />{item.targetName}</span>
          })}
          {baseView.showPacketLossArea && baseView.packetLossKey && packetLossSeries && (
            <span><i style={{ background: packetLossColor }} />{packetLossSeries.targetName} 丢包 {formatPercent(avgPacketLoss(lossRows, baseView.packetLossKey))}</span>
          )}
        </div>
      )}
    </section>
  )
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

function LatencyTooltip({ column, series, x: tooltipAnchorX, layout }: { column: HoverColumn; series: KulinTargetSeries[]; x: number; layout: typeof desktopLayout }) {
  const { width, height, pad } = layout
  const tooltipColumns = column.points.length > 6 ? 2 : 1
  const tooltipRows = Math.ceil(column.points.length / tooltipColumns)
  const tooltipWidth = tooltipColumns === 2 ? 390 : 270
  const rowHeight = 24
  const tooltipHeight = 58 + tooltipRows * rowHeight
  const tooltipX = Math.max(pad.left, Math.min(width - pad.right - tooltipWidth, tooltipAnchorX + 12))
  const plotHeight = height - pad.top - pad.bottom
  const centeredY = pad.top + Math.max(6, (plotHeight - tooltipHeight) / 2)
  const tooltipY = Math.max(pad.top + 4, Math.min(height - pad.bottom - tooltipHeight, centeredY))

  return (
    <g className="latency-chart-tooltip" transform={`translate(${tooltipX} ${tooltipY})`}>
      <foreignObject width={tooltipWidth} height={tooltipHeight}>
        <div className="latency-tooltip-card">
          <time>{formatTooltipTime(column.createdAt)}</time>
          <div className="latency-tooltip-grid" style={{ gridTemplateColumns: `repeat(${tooltipColumns}, minmax(0, 1fr))` }}>
            {column.points.map((point) => (
              <span key={`${point.key}-${column.createdAt}`} className="latency-tooltip-row">
                <i style={{ backgroundColor: latencySeriesColor(paletteIndexForKey(series, point.key)) }} />
                <b>{point.label}</b>
                <strong>{formatLatencyValue(point.delay)}</strong>
              </span>
            ))}
          </div>
        </div>
      </foreignObject>
    </g>
  )
}

function linePath(rows: KulinChartRow[], key: string, x: (createdAt: number) => number, y: (value: number) => number): string {
  let hasOpenSegment = false
  return rows
    .map((row) => {
      const value = rowNumber(row, key)
      if (value === null) {
        hasOpenSegment = false
        return ''
      }
      const command = hasOpenSegment ? 'L' : 'M'
      hasOpenSegment = true
      return `${command} ${x(row.created_at).toFixed(2)} ${y(value).toFixed(2)}`
    })
    .filter(Boolean)
    .join(' ')
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
  const value = row[key]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

interface HoverPoint {
  key: string
  label: string
  delay: number
}

interface HoverColumn {
  createdAt: number
  title: string
  points: HoverPoint[]
}

function hoverColumnsForRows(rows: KulinChartRow[], keys: string[], series: KulinTargetSeries[]): HoverColumn[] {
  const labelsByTargetId = new Map(series.map((target) => [target.targetId, target.targetName]))
  return rows
    .map((row) => {
      const points = keys
        .map((key) => {
          const delay = rowNumber(row, key)
          if (delay === null) return null
          return {
            key,
            label: labelsByTargetId.get(key) ?? key,
            delay,
          }
        })
        .filter((point): point is HoverPoint => point !== null)
      if (points.length === 0) return null
      const title = [
        formatTooltipTime(row.created_at),
        ...points.map((point) => `${point.label} · ${formatLatencyValue(point.delay)}`),
      ].join('\n')
      return { createdAt: row.created_at, title, points }
    })
    .filter((column): column is HoverColumn => column !== null)
}

function paletteIndexForKey(series: KulinTargetSeries[], key: string): number {
  const index = series.findIndex((item) => item.targetId === key)
  return Math.max(index, 0)
}

function formatLatencyValue(value: number): string {
  return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(2)}ms`
}

function formatTooltipTime(createdAt: number): string {
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return '--:--'
  return date.toLocaleString('zh-CN', { hour12: false })
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
  const date = new Date(createdAt)
  if (Number.isNaN(date.getTime())) return '--:--'
  const start = timestamps[0] ?? createdAt
  const end = timestamps.at(-1) ?? createdAt
  const spanHours = (end - start) / 3_600_000
  const time = `${date.getHours()}:${date.getMinutes().toString().padStart(2, '0')}`
  if (spanHours > 36) return `${date.getMonth() + 1}/${date.getDate()} ${time}`
  return time
}
