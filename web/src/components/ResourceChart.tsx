import { useMemo, type MouseEvent as ReactMouseEvent } from 'react'
import {
  downsampleSegments,
  formatAxisTime,
  formatResourceAxis,
  nearestIndex,
  resourceDomain,
  sampleCadenceMs,
  splitSegments,
  svgPointerX,
  type ResourceScale,
  type TimedValue,
} from '../lib/resourceChart'

export interface ResourceChartSeries {
  key: string
  label: string
  /** CSS colour, usually a metric custom property so dark mode follows. */
  color: string
  values: Array<number | null>
  /** Optional plot series when plotted values differ from tooltip values. */
  plot?: Array<number | null>
  /** Tooltip text for one sample, e.g. "46.9% · 2.7 GB / 5.8 GB". */
  describe: (index: number) => string
}

export interface ResourceChartExtraRow {
  label: string
  describe: (index: number) => string | null
}

function linePath(segment: TimedValue[], x: (t: number) => number, y: (v: number) => number): string {
  return segment.map((point, index) => `${index === 0 ? 'M' : 'L'}${x(point.t).toFixed(2)} ${y(point.v).toFixed(2)}`).join(' ')
}

interface ResourceMiniChartProps {
  label: string
  scale: ResourceScale
  series: ResourceChartSeries[]
  timestamps: number[]
  /** Shared hover sample index so every small chart shows the same moment. */
  hoverIndex: number | null
  onHoverIndexChange: (index: number | null) => void
}

const miniLayout = { width: 360, height: 122, pad: { left: 44, right: 6, top: 8, bottom: 22 } }

function areaPath(segment: TimedValue[], x: (t: number) => number, y: (v: number) => number, baseline: number): string {
  if (segment.length < 2) return ''
  const first = segment[0]
  const last = segment[segment.length - 1]
  return `M${x(first.t).toFixed(2)} ${baseline.toFixed(2)} ${segment.map((point) => `L${x(point.t).toFixed(2)} ${y(point.v).toFixed(2)}`).join(' ')} L${x(last.t).toFixed(2)} ${baseline.toFixed(2)} Z`
}

/**
 * One metric in its own unit. Small multiples replace the old mixed-unit chart:
 * hovering any of them moves the shared crosshair across all of them.
 */
export function ResourceMiniChart({ label, scale, series, timestamps, hoverIndex: rawHoverIndex, onHoverIndexChange }: ResourceMiniChartProps) {
  const { width, height, pad } = miniLayout
  const domain = useMemo(() => resourceDomain(series.flatMap((item) => item.plot ?? item.values), scale), [series, scale])
  const plotLeft = pad.left
  const plotRight = width - pad.right
  const plotWidth = plotRight - plotLeft
  const plotTop = pad.top
  const plotBottom = height - pad.bottom
  const plotHeight = plotBottom - plotTop
  const start = timestamps[0] ?? 0
  const end = timestamps.at(-1) ?? start
  const span = end - start
  const x = useMemo(() => (t: number) => (span > 0 ? plotLeft + ((t - start) / span) * plotWidth : plotRight), [plotLeft, plotRight, plotWidth, span, start])
  const y = useMemo(() => (v: number) => plotBottom - (Math.max(0, Math.min(domain.max, v)) / domain.max) * plotHeight, [domain.max, plotBottom, plotHeight])
  const ticks = [domain.ticks[0], domain.ticks[2], domain.ticks[4]]
  const { primaryAreas, renderedLines } = useMemo(() => {
    const cadence = sampleCadenceMs(timestamps)
    const gapMs = cadence > 0 ? cadence * 3 : 0
    const budget = Math.round(plotWidth * 1.5)
    const segmentsByKey = new Map(series.map((item) => [item.key, downsampleSegments(splitSegments(timestamps, item.plot ?? item.values, gapMs), budget)]))
    const primary = series[0]
    const areas = primary
      ? (segmentsByKey.get(primary.key) ?? []).map((segment) => areaPath(segment, x, y, plotBottom)).filter((d) => d !== '')
      : []
    const lines = series.map((item, seriesIndex) => ({
      key: item.key,
      color: item.color,
      dashed: seriesIndex > 0 && item.key === 'swap',
      shapes: (segmentsByKey.get(item.key) ?? []).map((segment) => (segment.length > 1
        ? { kind: 'path' as const, d: linePath(segment, x, y) }
        : { kind: 'dot' as const, cx: x(segment[0].t), cy: y(segment[0].v) })),
    }))
    return { primaryAreas: areas, renderedLines: lines }
  }, [series, timestamps, plotWidth, plotBottom, x, y])
  const hoverIndex = rawHoverIndex !== null && rawHoverIndex < timestamps.length ? rawHoverIndex : null
  const hoverX = hoverIndex !== null ? x(timestamps[hoverIndex]) : null
  const handleHover = (event: ReactMouseEvent<SVGRectElement>) => {
    if (timestamps.length === 0) return
    const svgX = svgPointerX(event.currentTarget, event.clientX, event.clientY)
    if (svgX === null) return
    const ratio = plotWidth > 0 ? Math.max(0, Math.min(1, (svgX - plotLeft) / plotWidth)) : 1
    onHoverIndexChange(nearestIndex(timestamps, start + ratio * span))
  }
  const axisIndexes = timestamps.length > 2 ? [0, Math.round((timestamps.length - 1) / 2), timestamps.length - 1] : timestamps.map((_, index) => index)
  const primary = series[0]

  return (
    <svg className="latency-chart resource-mini-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${label} 趋势`} onMouseLeave={() => onHoverIndexChange(null)}>
      {ticks.map((tick, index) => {
        const yy = y(tick)
        return (
          <g key={index}>
            <line x1={plotLeft} x2={plotRight} y1={yy} y2={yy} className="grid-line" />
            <text x={plotLeft - 6} y={yy + 3.5} className="axis-label" textAnchor="end">{formatResourceAxis(tick, scale, domain.max)}</text>
          </g>
        )
      })}
      {axisIndexes.map((index, position) => (
        <text key={index} x={x(timestamps[index])} y={height - 5} className="axis-label" textAnchor={position === 0 ? 'start' : position === axisIndexes.length - 1 ? 'end' : 'middle'}>
          {formatAxisTime(timestamps[index], span)}
        </text>
      ))}
      {primary && primaryAreas.map((d, index) => (
        <path key={`area-${index}`} className="resource-mini-area" d={d} style={{ fill: primary.color }} />
      ))}
      {renderedLines.map((item) => (
        <g key={item.key} data-series={item.key}>
          {item.shapes.map((shape, index) => (shape.kind === 'path'
            ? <path key={index} className={`resource-chart-line${item.dashed ? ' is-dashed' : ''}`} d={shape.d} style={{ stroke: item.color }} vectorEffect="non-scaling-stroke" />
            : <circle key={index} cx={shape.cx} cy={shape.cy} r={1.5} style={{ fill: item.color }} />))}
        </g>
      ))}
      {hoverX !== null && hoverIndex !== null && (
        <g className="latency-hover-column is-active">
          <line className="latency-hover-guide" x1={hoverX} x2={hoverX} y1={plotTop} y2={plotBottom} vectorEffect="non-scaling-stroke" />
          {series.map((item) => {
            const value = (item.plot ?? item.values)[hoverIndex]
            if (typeof value !== 'number' || !Number.isFinite(value)) return null
            return <circle key={item.key} className="latency-hover-point" cx={hoverX} cy={y(value)} r={3.2} style={{ fill: item.color }} />
          })}
        </g>
      )}
      <rect className="latency-hover-hit" x={plotLeft} y={plotTop} width={plotWidth} height={plotHeight} aria-hidden="true" onMouseEnter={handleHover} onMouseMove={handleHover} onClick={handleHover} />
    </svg>
  )
}
