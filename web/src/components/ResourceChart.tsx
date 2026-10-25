import { useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent, type ReactNode } from 'react'
import { formatClock, formatTooltipDateTime } from '../lib/format'
import {
  downsampleSegments,
  formatResourceAxis,
  nearestIndex,
  resourceDomain,
  sampleCadenceMs,
  splitSegments,
  type ResourceScale,
  type TimedValue,
} from '../lib/resourceChart'
import { axisTicksForTimestamps, clampAxisTickX, pruneCollidingAxisTicks } from './LatencyChart'
import { OverlaySurface } from './OverlaySurface'

export interface ResourceChartSeries {
  key: string
  label: string
  /** CSS colour, usually a metric custom property so dark mode follows. */
  color: string
  values: Array<number | null>
  /**
   * What is actually drawn when it differs from `values`: in a mixed-unit view
   * rates and counts are rescaled to 0-100 of their own peak. Tooltips always
   * describe the real value.
   */
  plot?: Array<number | null>
  /** Tooltip text for one sample, e.g. "46.9% · 2.7 GB / 5.8 GB". */
  describe: (index: number) => string
}

export interface ResourceChartExtraRow {
  label: string
  describe: (index: number) => string | null
}

interface ResourceChartProps {
  label: string
  scale: ResourceScale
  series: ResourceChartSeries[]
  /** Uncoloured tooltip rows that have no line, e.g. the CPU load averages. */
  extraRows?: ResourceChartExtraRow[]
  timestamps: number[]
}

// Same canvas and paddings as the latency chart so the two panels read alike.
const desktopLayout = { width: 960, height: 360, pad: { left: 60, right: 24, top: 24, bottom: 44 } }
const mobileLayout = { width: 400, height: 320, pad: { left: 50, right: 16, top: 22, bottom: 44 } }
type ChartLayout = typeof desktopLayout

export function ResourceChart({ label, scale, series, extraRows = [], timestamps }: ResourceChartProps) {
  const base = useResourceChartLayout()
  const domain = useMemo(() => resourceDomain(series.flatMap((item) => item.plot ?? item.values), scale), [series, scale])
  const yLabels = domain.ticks.map((tick) => formatResourceAxis(tick, scale, domain.max))
  const isMobile = base.width <= 480
  const labelCharWidth = isMobile ? 6 : 7.2
  // Widen the left padding only when a long rate label (e.g. "750 KB/s") needs it.
  const padLeft = Math.max(base.pad.left, Math.ceil(Math.max(...yLabels.map((text) => text.length)) * labelCharWidth) + 10)
  const layout: ChartLayout = { ...base, pad: { ...base.pad, left: padLeft } }
  const { width, height, pad } = layout
  const plotLeft = pad.left
  const plotRight = width - pad.right
  const plotWidth = plotRight - plotLeft
  const plotTop = pad.top
  const plotBottom = height - pad.bottom
  const plotHeight = plotBottom - plotTop

  const start = timestamps[0] ?? 0
  const end = timestamps.at(-1) ?? start
  const span = end - start
  const x = (t: number) => (span > 0 ? plotLeft + ((t - start) / span) * plotWidth : plotRight)
  const y = (v: number) => plotBottom - (Math.max(0, Math.min(domain.max, v)) / domain.max) * plotHeight

  const segmentsByKey = useMemo(() => {
    const cadence = sampleCadenceMs(timestamps)
    const gapMs = cadence > 0 ? cadence * 3 : 0
    const budget = Math.round(plotWidth * 1.5)
    return new Map(series.map((item) => [item.key, downsampleSegments(splitSegments(timestamps, item.plot ?? item.values, gapMs), budget)]))
  }, [series, timestamps, plotWidth])

  // Time labels follow the latency chart: a fixed step anchored on the newest sample.
  const candidateTicks = useMemo(() => axisTicksForTimestamps(timestamps, isMobile ? 4 : 14), [timestamps, isMobile])
  // Labels sit on the data's own time scale; a nominal tick just before the
  // oldest sample is clamped to the left edge rather than stretching the axis.
  const axisX = x
  const anchorOf = (t: number): 'start' | 'middle' | 'end' => {
    const xx = axisX(t)
    if (xx <= plotLeft + 8) return 'start'
    if (xx >= plotRight - 8) return 'end'
    return 'middle'
  }
  const axisTicks = pruneCollidingAxisTicks(
    candidateTicks,
    (t) => clampAxisTickX(axisX(t), width, pad),
    (t) => formatAxisTime(t, span),
    labelCharWidth,
    anchorOf,
  )

  const [rawHoverIndex, setHoverIndex] = useState<number | null>(null)
  // Live updates may shrink the series; never point past its end.
  const hoverIndex = rawHoverIndex !== null && rawHoverIndex < timestamps.length ? rawHoverIndex : null
  const handleHover = (event: ReactMouseEvent<SVGRectElement>) => {
    const svg = event.currentTarget.ownerSVGElement
    const ctm = svg?.getScreenCTM()
    if (!svg || !ctm || timestamps.length === 0) return
    const point = svg.createSVGPoint()
    point.x = event.clientX
    point.y = event.clientY
    const svgX = point.matrixTransform(ctm.inverse()).x
    const ratio = plotWidth > 0 ? Math.max(0, Math.min(1, (svgX - plotLeft) / plotWidth)) : 1
    const index = nearestIndex(timestamps, start + ratio * span)
    setHoverIndex((current) => (current === index ? current : index))
  }

  const hoverX = hoverIndex !== null ? x(timestamps[hoverIndex]) : null

  return (
    <section className="latency-panel is-compact resource-chart-panel">
      <svg className="latency-chart resource-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${label} 趋势`} onMouseLeave={() => setHoverIndex(null)}>
        {domain.ticks.map((tick, index) => {
          const yy = y(tick)
          return (
            <g key={index}>
              <line x1={plotLeft} x2={plotRight} y1={yy} y2={yy} className="grid-line" />
              <text x={plotLeft - 6} y={yy + 4} className="axis-label" textAnchor="end">{yLabels[index]}</text>
            </g>
          )
        })}
        {axisTicks.map((tick) => (
          <text key={tick} x={clampAxisTickX(axisX(tick), width, pad)} y={height - 12} className="axis-label" textAnchor={anchorOf(tick)}>
            {formatAxisTime(tick, span)}
          </text>
        ))}

        {series.map((item) => (
          <g key={item.key} data-series={item.key}>
            {(segmentsByKey.get(item.key) ?? []).map((segment, index) => (segment.length > 1
              ? <path key={index} className="resource-chart-line" d={linePath(segment, x, y)} style={{ stroke: item.color }} vectorEffect="non-scaling-stroke" />
              : <circle key={index} cx={x(segment[0].t)} cy={y(segment[0].v)} r={1.5} style={{ fill: item.color }} />))}
          </g>
        ))}

        {hoverX !== null && hoverIndex !== null && (
          <g className="latency-hover-column is-active">
            <line className="latency-hover-guide" x1={hoverX} x2={hoverX} y1={plotTop} y2={plotBottom} vectorEffect="non-scaling-stroke" />
            {series.map((item) => {
              const value = (item.plot ?? item.values)[hoverIndex]
              if (typeof value !== 'number' || !Number.isFinite(value)) return null
              return <circle key={item.key} className="latency-hover-point" cx={hoverX} cy={y(value)} r={5} fill={item.color} style={{ fill: item.color }} />
            })}
          </g>
        )}
        <rect
          className="latency-hover-hit"
          x={plotLeft}
          y={plotTop}
          width={plotWidth}
          height={plotHeight}
          aria-hidden="true"
          onMouseEnter={handleHover}
          onMouseMove={handleHover}
          onClick={handleHover}
        />
        {hoverX !== null && hoverIndex !== null && (
          <ResourceTooltip layout={layout} anchorX={hoverX} time={timestamps[hoverIndex]} rows={series.length + extraRows.length}>
            {series.map((item) => (
              <span key={item.key} className="latency-tooltip-row">
                <i style={{ backgroundColor: item.color }} />
                <b>{item.label}</b>
                <strong>{item.describe(hoverIndex)}</strong>
              </span>
            ))}
            {extraRows.map((row) => {
              const text = row.describe(hoverIndex)
              return text === null ? null : (
                <span key={row.label} className="latency-tooltip-row">
                  <i aria-hidden="true" />
                  <b>{row.label}</b>
                  <strong>{text}</strong>
                </span>
              )
            })}
          </ResourceTooltip>
        )}
      </svg>
    </section>
  )
}

function ResourceTooltip({ layout, anchorX, time, rows, children }: { layout: ChartLayout; anchorX: number; time: number; rows: number; children: ReactNode }) {
  const { width, height, pad } = layout
  const plotWidth = width - pad.left - pad.right
  const plotHeight = height - pad.top - pad.bottom
  // Same rule as the latency tooltip: many rows wrap into two columns.
  const columns = rows > (width <= 480 ? 4 : 6) ? 2 : 1
  const tooltipWidth = Math.min(columns === 2 ? 440 : 290, plotWidth)
  // Prefer the right of the guide; flip left near the right edge, as the latency tooltip does.
  const tooltipX = Math.max(pad.left, Math.min(width - pad.right - tooltipWidth, anchorX + 12))
  return (
    <g className="latency-chart-tooltip" transform={`translate(${tooltipX} ${pad.top + 4})`}>
      <foreignObject width={tooltipWidth} height={plotHeight - 8}>
        <div className="latency-tooltip-viewport">
          <OverlaySurface className="latency-tooltip-card">
            <time>{formatTooltipDateTime(time)}</time>
            <div className="latency-tooltip-grid" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>{children}</div>
          </OverlaySurface>
        </div>
      </foreignObject>
    </g>
  )
}

function useResourceChartLayout(): ChartLayout {
  const query = '(max-width: 767px)'
  const [isMobile, setIsMobile] = useState(() => typeof window !== 'undefined' && window.matchMedia(query).matches)
  useEffect(() => {
    if (typeof window === 'undefined') return undefined
    const media = window.matchMedia(query)
    const update = () => setIsMobile(media.matches)
    update()
    media.addEventListener('change', update)
    return () => media.removeEventListener('change', update)
  }, [])
  return isMobile ? mobileLayout : desktopLayout
}

function linePath(segment: TimedValue[], x: (t: number) => number, y: (v: number) => number): string {
  return segment.map((point, index) => `${index === 0 ? 'M' : 'L'}${x(point.t).toFixed(2)} ${y(point.v).toFixed(2)}`).join(' ')
}

function formatAxisTime(timestamp: number, spanMs: number): string {
  const date = new Date(timestamp)
  if (Number.isNaN(date.getTime())) return '--:--'
  const time = formatClock(date)
  if (spanMs > 36 * 3_600_000) return `${date.getMonth() + 1}/${date.getDate()} ${time}`
  return time
}
