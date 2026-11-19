import { describe, expect, it } from 'vitest'
import {
  applyKulinPeakCut,
  buildKulinChartRows,
  buildKulinTargetSeries,
  calculateKulinPacketLoss,
  selectKulinChartView,
} from './kulinLatencyChart'

describe('calculateKulinPacketLoss', () => {
  it('matches Kulin packet-loss smoothing for zero and timeout-like delays', () => {
    const losses = calculateKulinPacketLoss([15, 18, 0, 45])

    expect(losses).toEqual([0, 0, 30, 21])
  })
})

describe('buildKulinTargetSeries', () => {
  it('uses avg_ms as Kulin avg_delay and leaves missing or timed-out samples as gaps', () => {
    const series = buildKulinTargetSeries([
      { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 14, lossPercent: 0 },
      { ts: '2026-07-02T00:01:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 99, lossPercent: 100 },
      { ts: '2026-07-02T00:02:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 5000, avgMs: 5000, lossPercent: 100 },
    ])

    expect(series[0].points.map((point) => point.avg_delay)).toEqual([14, null, null])
    expect(series[0].points.map((point) => point.packet_loss)).toEqual([0, 100, 100])
  })

  it('reuses normalized millisecond timestamps while preserving out-of-order fallback sorting', () => {
    const series = buildKulinTargetSeries([
      { ts: 'ignored-later', tsMs: 2000, targetId: 'alpha', targetName: 'Alpha', medianMs: 20, avgMs: 20, lossPercent: 0 },
      { ts: 'ignored-earlier', tsMs: 1000, targetId: 'alpha', targetName: 'Alpha', medianMs: 10, avgMs: 10, lossPercent: Number.NaN },
    ])

    expect(series[0].points.map((point) => point.created_at)).toEqual([1000, 2000])
    expect(series[0].points[0].packet_loss).toBe(0)
  })
})

describe('buildKulinChartRows', () => {
  it('merges target series by timestamp using target ids as Kulin chart data keys', () => {
    const series = buildKulinTargetSeries([
      { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 12, lossPercent: 0 },
      { ts: '2026-07-02T00:01:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, avgMs: 20, lossPercent: 5 },
    ])

    expect(buildKulinChartRows(series)).toEqual([
      { created_at: Date.parse('2026-07-02T00:00:00Z'), alpha: 12, alpha_packet_loss: 0, beta: null, beta_packet_loss: null },
      { created_at: Date.parse('2026-07-02T00:01:00Z'), alpha: null, alpha_packet_loss: null, beta: 20, beta_packet_loss: 5 },
    ])
  })

  it('keeps duplicate and reserved-looking target names as labels without overwriting row fields', () => {
    const timestamp = '2026-07-02T00:00:00Z'
    const series = buildKulinTargetSeries([
      { ts: timestamp, targetId: 'first-id', targetName: 'Duplicate', medianMs: 11, avgMs: 11, lossPercent: 1 },
      { ts: timestamp, targetId: 'second-id', targetName: 'Duplicate', medianMs: 22, avgMs: 22, lossPercent: 2 },
      { ts: timestamp, targetId: 'created-id', targetName: 'created_at', medianMs: 33, avgMs: 33, lossPercent: 3 },
      { ts: timestamp, targetId: 'loss-id', targetName: 'first-id_packet_loss', medianMs: 44, avgMs: 44, lossPercent: 4 },
    ])

    expect(series.map((target) => target.targetName)).toEqual(['Duplicate', 'Duplicate', 'created_at', 'first-id_packet_loss'])
    expect(buildKulinChartRows(series)).toEqual([{
      created_at: Date.parse(timestamp),
      'first-id': 11,
      'first-id_packet_loss': 1,
      'second-id': 22,
      'second-id_packet_loss': 2,
      'created-id': 33,
      'created-id_packet_loss': 3,
      'loss-id': 44,
      'loss-id_packet_loss': 4,
    }])
  })
})

describe('selectKulinChartView', () => {
  const series = buildKulinTargetSeries([
    { ts: '2026-07-02T00:00:00Z', targetId: 'alpha', targetName: 'Alpha', medianMs: 12, avgMs: 12, lossPercent: 0 },
    { ts: '2026-07-02T00:00:00Z', targetId: 'beta', targetName: 'Beta', medianMs: 20, avgMs: 20, lossPercent: 5 },
  ])
  const rows = buildKulinChartRows(series)

  it('shows all delay lines by default like Kulin', () => {
    const view = selectKulinChartView(series, rows, [])

    expect(view.lineKeys).toEqual(['alpha', 'beta'])
    expect(view.showPacketLossArea).toBe(false)
    expect(view.packetLossKey).toBeNull()
  })

  it('shows selected delay plus packet-loss area when exactly one monitor is active', () => {
    const view = selectKulinChartView(series, rows, ['alpha'])

    expect(view.lineKeys).toEqual(['alpha'])
    expect(view.showPacketLossArea).toBe(true)
    expect(view.packetLossKey).toBe('alpha_packet_loss')
    expect(view.rows).toEqual([{
      created_at: Date.parse('2026-07-02T00:00:00Z'),
      alpha: 12,
      alpha_packet_loss: 0,
    }])
  })
})

describe('applyKulinPeakCut', () => {
  it('uses Kulin 11-point MAD/EWMA smoothing to suppress a single spike', () => {
    const rows = Array.from({ length: 11 }, (_, index) => ({
      created_at: index,
      alpha: index === 10 ? 1000 : 10,
    }))

    const processed = applyKulinPeakCut(rows, ['alpha'])

    expect(processed.slice(0, 10).map((row) => row.alpha)).toEqual(Array(10).fill(10))
    expect(processed[10].alpha).toBe(10)
  })

  it('suppresses leading spikes in the first ten samples so smoothed charts do not pin to 100% at the left edge', () => {
    const rows = Array.from({ length: 12 }, (_, index) => ({
      created_at: index,
      alpha: index === 0 ? 5000 : 12,
    }))

    const processed = applyKulinPeakCut(rows, ['alpha'])

    expect(processed[0].alpha).toBeCloseTo(12, 10)
    expect(Math.max(...processed.map((row) => row.alpha ?? 0))).toBeCloseTo(12, 10)
  })
})
