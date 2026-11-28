import type { LatencyPoint } from '../types'
import { isFiniteNumber } from './number'

export interface LatencyTargetSummary {
  targetId: string
  targetName: string
  sampleCount: number
  avgMs: number | null
  lossPercent: number | null
}

interface Accumulator {
  targetId: string
  targetName: string
  sampleCount: number
  latestDelay: number | null
  lossTotal: number
  lossCount: number
}

export function summarizeLatencyTargets(points: LatencyPoint[]): LatencyTargetSummary[] {
  const byTarget = new Map<string, Accumulator>()

  for (const point of points) {
    const existing = byTarget.get(point.targetId)
    const acc = existing ?? {
      targetId: point.targetId,
      targetName: point.targetName,
      sampleCount: 0,
      latestDelay: null,
      lossTotal: 0,
      lossCount: 0,
    }

    acc.targetName = point.targetName
    const delay = point.avgMs
    const hasDelay = isFiniteNumber(delay)
    const missingBucket = !hasDelay && point.lossPercent === 0
    if (!missingBucket) acc.sampleCount += 1
    if (hasDelay) {
      acc.latestDelay = delay
    }
    if (!missingBucket && Number.isFinite(point.lossPercent)) {
      acc.lossTotal += point.lossPercent
      acc.lossCount += 1
    }

    if (!existing) byTarget.set(point.targetId, acc)
  }

  return [...byTarget.values()].map((acc) => ({
    targetId: acc.targetId,
    targetName: acc.targetName,
    sampleCount: acc.sampleCount,
    avgMs: acc.latestDelay !== null ? round2(acc.latestDelay) : null,
    lossPercent: acc.lossCount > 0 ? round2(acc.lossTotal / acc.lossCount) : null,
  }))
}

function round2(value: number): number {
  return Math.round(value * 100) / 100
}
