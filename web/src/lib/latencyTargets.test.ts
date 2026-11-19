import { describe, expect, it } from 'vitest'
import type { LatencyPoint } from '../types'
import { summarizeLatencyTargets } from './latencyTargets'

const points: LatencyPoint[] = [
  { ts: '2026-07-02T12:00:00Z', targetId: 'google', targetName: 'Google', medianMs: 1, avgMs: 1.5, lossPercent: 0 },
  { ts: '2026-07-02T12:00:00Z', targetId: 'dc1', targetName: 'DC1', medianMs: null, lossPercent: 100 },
  { ts: '2026-07-02T12:02:00Z', targetId: 'google', targetName: 'Google', medianMs: 3, avgMs: 3, lossPercent: 2 },
  { ts: '2026-07-02T12:02:00Z', targetId: 'dc1', targetName: 'DC1', medianMs: 180, avgMs: 180, lossPercent: 0 },
]

describe('summarizeLatencyTargets', () => {
  it('keeps first-seen target order and displays Kulin-style latest delay plus average packet loss', () => {
    const targets = summarizeLatencyTargets(points)

    expect(targets.map((target) => target.targetId)).toEqual(['google', 'dc1'])
    expect(targets[0]).toMatchObject({
      targetId: 'google',
      targetName: 'Google',
      sampleCount: 2,
      avgMs: 3,
      lossPercent: 1,
    })
    expect(targets[1]).toMatchObject({
      targetId: 'dc1',
      targetName: 'DC1',
      sampleCount: 2,
      avgMs: 180,
      lossPercent: 50,
    })
  })

  it('keeps all-loss samples latency empty while preserving packet loss', () => {
    const targets = summarizeLatencyTargets([
      { ts: '2026-07-02T12:00:00Z', targetId: 'dc2', targetName: 'DC2', medianMs: null, lossPercent: 100 },
      { ts: '2026-07-02T12:02:00Z', targetId: 'dc2', targetName: 'DC2', medianMs: null, lossPercent: 100 },
    ])

    expect(targets).toEqual([
      {
        targetId: 'dc2',
        targetName: 'DC2',
        sampleCount: 2,
        avgMs: null,
        lossPercent: 100,
      },
    ])
  })

  it('does not count empty grid buckets as zero-loss samples', () => {
    const targets = summarizeLatencyTargets([
      { ts: '2026-07-02T12:00:00Z', targetId: 'dc2', targetName: 'DC2', medianMs: null, avgMs: null, lossPercent: 0 },
      { ts: '2026-07-02T12:01:00Z', targetId: 'dc2', targetName: 'DC2', medianMs: 190, avgMs: 190, lossPercent: 10 },
    ])

    expect(targets[0]).toMatchObject({ sampleCount: 1, avgMs: 190, lossPercent: 10 })
  })

  it('keeps packet loss null when every bucket for a target is empty', () => {
    const targets = summarizeLatencyTargets([
      { ts: '2026-07-02T12:00:00Z', targetId: 'dc2', targetName: 'DC2', medianMs: null, avgMs: null, lossPercent: 0 },
    ])

    expect(targets[0]).toMatchObject({ sampleCount: 0, avgMs: null, lossPercent: null })
  })
})
