import { latencySeriesColor } from '../lib/seriesColors'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { integerYAxisDomain, LatencyChart, pruneCollidingAxisTicks, yDomainForRows } from './LatencyChart'

const points = [
  { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 12, lossPercent: 0 },
  { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 42, avgMs: 42, lossPercent: 25 },
  { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, avgMs: 20, lossPercent: 5 },
]

describe('LatencyChart', () => {
  it('renders a single custom hover surface without browser title tooltips', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha', 'beta']} />)

    expect(html).toContain('latency-hover-hit')
    expect(html.match(/latency-hover-hit/g)).toHaveLength(1)
    expect(html).not.toContain('<title>')
    expect(html).toContain('aria-label=')
    expect(html).toContain('延迟图表悬浮区域')
    expect(html).not.toContain('丢包 25.00%')
  })

  it('shows packet loss only for a single selected target, not when multiple latency lines are displayed', () => {
    const multiHtml = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha', 'beta']} />)
    const singleHtml = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha']} />)

    expect(multiHtml).not.toContain('packet-loss-area')
    expect(multiHtml).not.toContain('丢包')
    expect(singleHtml).toContain('packet-loss-area')
    expect(singleHtml).toContain('Alpha 丢包')
  })

  it('can hide the chart legend when an external target picker already provides the same labels', () => {
    const html = renderToStaticMarkup(<LatencyChart points={points} activeTargetIds={['alpha']} hideLegend />)

    expect(html).not.toContain('latency-legend')
    expect(html).toContain('packet-loss-area')
  })

  it('anchors every day-range tick to the newest sample minute', () => {
    const dayPoints = Array.from({ length: 49 }, (_, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, 30) + index * 30 * 60 * 1000).toISOString(),
      targetId: 'alpha',
      targetName: 'Alpha',
      medianMs: 20 + index,
      avgMs: 20 + index,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={dayPoints} activeTargetIds={['alpha']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const xAxisLabels = labels.filter((label) => label.includes(':'))

    expect(xAxisLabels.length).toBeGreaterThan(2)
    // The newest sample is at :30, so walking backwards by a fixed step must
    // preserve :30 on every label instead of snapping the sequence to :00.
    expect(xAxisLabels.every((label) => label.endsWith(':30'))).toBe(true)
    expect(new Set(xAxisLabels).size).toBeGreaterThan(2)
  })

  it('auto-scales the delay axis like Kulin instead of anchoring every selected target to zero', () => {
    const steadyHighLatencyPoints = [186.5, 187.2, 188.0].map((medianMs, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, index * 30)).toISOString(),
      targetId: 'high',
      targetName: 'High latency',
      medianMs,
      avgMs: medianMs,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={steadyHighLatencyPoints} activeTargetIds={['high']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const yAxisLabels = labels.filter((label) => label.endsWith('ms'))

    expect(yAxisLabels).not.toContain('0 ms')
    expect(yAxisLabels.some((label) => label.startsWith('186') || label.startsWith('187') || label.startsWith('188'))).toBe(true)
  })

  it('uses a minimum four millisecond axis span to keep narrow-range labels distinct integers', () => {
    const narrowLatencyPoints = [144, 144.2, 144.5, 144.8, 145].map((medianMs, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, index * 30)).toISOString(),
      targetId: 'fiber-state',
      targetName: 'FiberState',
      medianMs,
      avgMs: medianMs,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={narrowLatencyPoints} activeTargetIds={['fiber-state']} />)
    const yAxisLabels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)]
      .map((match) => match[1])
      .filter((label) => label.endsWith('ms'))

    expect(yAxisLabels).toHaveLength(5)
    expect(new Set(yAxisLabels).size).toBe(5)
    expect(yAxisLabels.every((label) => /^\d+ ms$/.test(label))).toBe(true)
    const yAxisValues = yAxisLabels.map((label) => Number(label.replace('ms', '')))
    expect(Math.max(...yAxisValues) - Math.min(...yAxisValues)).toBeGreaterThanOrEqual(4)
    expect(yAxisValues.every((value, index) => index === 0 || Number.isInteger(yAxisValues[index - 1] - value))).toBe(true)
  })

  it('caps drawn latency axis at 5000ms while still allowing values above the 1000ms timeout line', () => {
    const highLatencyPoints = [900, 2400, 6000].map((medianMs, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, index * 30)).toISOString(),
      targetId: 'high',
      targetName: 'High latency',
      medianMs,
      avgMs: medianMs,
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={highLatencyPoints} activeTargetIds={['high']} />)
    const labels = [...html.matchAll(/class="axis-label"[^>]*>([^<]+)<\/text>/g)].map((match) => match[1])
    const yAxisValues = labels
      .filter((label) => label.endsWith('ms'))
      .map((label) => Number(label.replace('ms', '')))

    expect(yAxisValues.reduce((max, value) => Math.max(max, value), Number.NEGATIVE_INFINITY)).toBe(5000)
    expect(yAxisValues.some((value) => value > 1000)).toBe(true)
  })

  it('breaks the line at null avg_ms samples without falling back to median_ms', () => {
    const gapPoints = [
      { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', avgMs: 12, medianMs: 10, lossPercent: 0 },
      { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', avgMs: null, medianMs: 999, lossPercent: 0 },
      { ts: '2026-07-02T00:02:00Z', targetId: 'alpha', targetName: 'Alpha', avgMs: 18, medianMs: 16, lossPercent: 0 },
    ]

    const html = renderToStaticMarkup(<LatencyChart points={gapPoints} activeTargetIds={['alpha']} />)
    const lineMatch = html.match(new RegExp(`<path[^>]+d="([^"]+)"[^>]+stroke="${latencySeriesColor(0)}"`))

    expect(lineMatch?.[1].match(/M/g)).toHaveLength(2)
    expect(lineMatch?.[1].match(/L/g) ?? []).toHaveLength(0)
  })

  it('keeps the full Kulin-style 1 day minute grid instead of thinning visible points', () => {
    const minutePoints = Array.from({ length: 1440 }, (_, index) => ({
      ts: new Date(Date.UTC(2026, 6, 5, 0, 0) + index * 60 * 1000).toISOString(),
      targetId: 'alpha',
      targetName: 'Alpha',
      avgMs: 20 + (index % 10),
      medianMs: 20 + (index % 10),
      lossPercent: 0,
    }))

    const html = renderToStaticMarkup(<LatencyChart points={minutePoints} activeTargetIds={['alpha']} />)
    const lineMatch = html.match(new RegExp(`<path[^>]+d="([^"]+)"[^>]+stroke="${latencySeriesColor(0)}"`))

    expect(lineMatch?.[1].match(/M/g)).toHaveLength(1)
    expect(lineMatch?.[1].match(/L/g)).toHaveLength(1439)
  })

  it('renders duplicate and reserved-looking target names as labels while selecting lines by id', () => {
    const timestamp = '2026-07-02T00:00:00Z'
    const specialPoints = [
      { ts: timestamp, targetId: 'first-id', targetName: 'Duplicate', avgMs: 11, medianMs: 11, lossPercent: 0 },
      { ts: timestamp, targetId: 'second-id', targetName: 'Duplicate', avgMs: 22, medianMs: 22, lossPercent: 0 },
      { ts: timestamp, targetId: 'created-id', targetName: 'created_at', avgMs: 33, medianMs: 33, lossPercent: 0 },
      { ts: timestamp, targetId: 'loss-id', targetName: 'first-id_packet_loss', avgMs: 44, medianMs: 44, lossPercent: 0 },
    ]

    const html = renderToStaticMarkup(
      <LatencyChart points={specialPoints} activeTargetIds={specialPoints.map((point) => point.targetId)} />,
    )

    expect(html.match(/>Duplicate<\/span>/g)).toHaveLength(2)
    expect(html).toContain('>created_at</span>')
    expect(html).toContain('>first-id_packet_loss</span>')
    expect(html.match(/<path[^>]+vector-effect="non-scaling-stroke"/g)).toHaveLength(4)
  })

  it('reduces an exceptionally large latency payload without spreading it into Math.min or Math.max', () => {
    const rows = Array.from({ length: 250_000 }, (_, index) => ({ created_at: index, target: index % 2 === 0 ? 12 : 48 }))

    expect(() => yDomainForRows(rows, ['target'])).not.toThrow()
    const domain = yDomainForRows(rows, ['target'])
    expect(domain.min).toBeCloseTo(6.6)
    expect(domain.max).toBeCloseTo(53.4)
  })

  it('keeps integer tick endpoints within the 5000ms cap', () => {
    expect(integerYAxisDomain({ min: 143.8, max: 145.3 })).toEqual({ min: 142, max: 146 })
    expect(integerYAxisDomain({ min: 4998.8, max: 5000 })).toEqual({ min: 4996, max: 5000 })
  })
})

describe('pruneCollidingAxisTicks endpoint reservation', () => {
  const charWidth = 7.2
  const label = (t: number) => (t === 0 ? '18:44' : t === 999 ? '18:43' : '12:00')
  const anchor = (t: number) => (t === 0 ? 'start' as const : t === 999 ? 'end' as const : 'middle' as const)

  // The endpoints state the axis range (oldest sample and "now"); interior marks
  // are interchangeable round hours. Dropping an endpoint silently changes what
  // range the chart appears to cover, so it must never be the sacrifice.
  it('keeps both endpoints and drops the colliding interior tick instead', () => {
    const kept = pruneCollidingAxisTicks(
      [0, 1, 999],
      (t) => (t === 0 ? 52 : t === 1 ? 70 : 936),
      label,
      charWidth,
      anchor,
    )
    expect(kept).toEqual([0, 999])
  })

  it('still returns both endpoints when they collide with each other', () => {
    const kept = pruneCollidingAxisTicks(
      [0, 1, 999],
      (t) => (t === 0 ? 52 : t === 1 ? 60 : 70),
      label,
      charWidth,
      anchor,
    )
    expect(kept).toEqual([0, 999])
  })

  it('rejects an interior tick that clears its right neighbour but overlaps the first tick', () => {
    // 52 'start' occupies 52..88; an interior label centred at 92 spans 74..110,
    // so it clears the far right endpoint yet still collides with the first.
    const kept = pruneCollidingAxisTicks(
      [0, 1, 999],
      (t) => (t === 0 ? 52 : t === 1 ? 92 : 936),
      label,
      charWidth,
      anchor,
    )
    expect(kept).toEqual([0, 999])
  })
})
