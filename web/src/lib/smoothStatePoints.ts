import type { StatePoint } from '../types'
import { applyKulinPeakCut, type KulinChartRow } from './kulinLatencyChart'

// Sampled values worth smoothing; capacities (memory/swap/disk totals) and
// counters stay as reported.
const smoothedFields = [
  'cpuPercent',
  'load1',
  'load5',
  'load15',
  'memoryUsedBytes',
  'swapUsedBytes',
  'diskUsedBytes',
  'netInSpeedBps',
  'netOutSpeedBps',
  'processCount',
  'tcpConnectionCount',
  'udpConnectionCount',
] as const satisfies ReadonlyArray<keyof StatePoint>

/**
 * Same peak cut + EWMA as the latency chart's “平滑” mode. Missing samples
 * stay missing, so offline stretches are still drawn as gaps.
 */
export function smoothStatePoints(points: StatePoint[]): StatePoint[] {
  const rows: KulinChartRow[] = points.map((point) => {
    const row: KulinChartRow = { created_at: Date.parse(point.ts) }
    for (const field of smoothedFields) row[field] = point[field]
    return row
  })
  const smoothed = applyKulinPeakCut(rows, [...smoothedFields])
  return points.map((point, index) => {
    const next: StatePoint = { ...point }
    for (const field of smoothedFields) {
      const original = point[field]
      const value = smoothed[index][field]
      if (typeof original === 'number' && Number.isFinite(original) && typeof value === 'number' && Number.isFinite(value)) next[field] = value
    }
    return next
  })
}
