import { describe, expect, it } from 'vitest'
import { axisTicksForTimestamps, clampAxisTickX, pruneCollidingAxisTicks } from './LatencyChart'

// Exact geometry captured from https://siaoz.com/server/datawave-hk at a
// 1440px viewport: viewBox 960x320, 14 axis ticks, real rendered char width
// 6.904px at 12px font. This is the case 水饺 reported as overlapping.
const PROD = [
  { txt: '18:44', x: 52, anchor: 'start' as const },
  { txt: '20:00', x: 98.68797776233495, anchor: 'middle' as const },
  { txt: '22:00', x: 172.40583738707437, anchor: 'middle' as const },
  { txt: '0:00', x: 246.12369701181376, anchor: 'middle' as const },
  { txt: '2:00', x: 319.84155663655315, anchor: 'middle' as const },
  { txt: '4:00', x: 393.5594162612926, anchor: 'middle' as const },
  { txt: '6:00', x: 467.277275886032, anchor: 'middle' as const },
  { txt: '8:00', x: 540.9951355107714, anchor: 'middle' as const },
  { txt: '10:00', x: 614.7129951355107, anchor: 'middle' as const },
  { txt: '12:00', x: 688.4308547602502, anchor: 'middle' as const },
  { txt: '14:00', x: 762.1487143849896, anchor: 'middle' as const },
  { txt: '16:00', x: 835.866574009729, anchor: 'middle' as const },
  { txt: '18:00', x: 909.5844336344684, anchor: 'middle' as const },
  { txt: '18:43', x: 936, anchor: 'end' as const },
]

const WIDTH = 960
const PAD = { left: 52, right: 24 }
const CHAR = 7.2

function extent(centre: number, text: string, anchor: 'start' | 'middle' | 'end') {
  const w = text.length * CHAR
  if (anchor === 'start') return { left: centre, right: centre + w }
  if (anchor === 'end') return { left: centre - w, right: centre }
  return { left: centre - w / 2, right: centre + w / 2 }
}

function gapsFor(indices: number[]) {
  const gaps: { between: [string, string]; gap: number }[] = []
  for (let i = 1; i < indices.length; i += 1) {
    const p = PROD[indices[i - 1]]
    const c = PROD[indices[i]]
    gaps.push({
      between: [p.txt, c.txt],
      gap: +(extent(c.x, c.txt, c.anchor).left - extent(p.x, p.txt, p.anchor).right).toFixed(1),
    })
  }
  return gaps
}

describe('production axis geometry that 水饺 reported', () => {
  it('confirms the unfixed axis really does overlap at both ends', () => {
    const overlaps = gapsFor(PROD.map((_, i) => i)).filter((g) => g.gap < 0)
    expect(overlaps.map((o) => o.between.join('/'))).toEqual(['18:44/20:00', '18:00/18:43'])
  })

  it('removes every overlap while keeping the newest label', () => {
    const ticks = PROD.map((_, i) => i)
    const kept = pruneCollidingAxisTicks(
      ticks,
      (t) => clampAxisTickX(PROD[t].x, WIDTH, PAD),
      (t) => PROD[t].txt,
      CHAR,
      (t) => PROD[t].anchor,
    )
    const gaps = gapsFor(kept)
    const worst = Math.min(...gaps.map((g) => g.gap))

    expect(gaps.filter((g) => g.gap < 0)).toEqual([])
    expect(worst).toBeGreaterThanOrEqual(6)
    // Both endpoints state the axis range and must survive; only interior
    // round-hour marks may be sacrificed.
    expect(PROD[kept[0]].txt).toBe('18:44')
    expect(PROD[kept[kept.length - 1]].txt).toBe('18:43')
  })
})

// The current time is the reading anchor. If the newest point is 19:31 and the
// chosen step is two hours, every preceding label must be :31 as well. Pinning
// that exact contract prevents wall-clock alignment from returning unnoticed.
describe('axis ticks walk backwards evenly from the newest sample', () => {
  const minute = 60_000
  const width = 960
  const pad = { left: 52, right: 24 }
  const charWidth = 7.2

  const end = new Date('2026-07-31T19:31:00+08:00').getTime()
  const timestamps = Array.from({ length: 1440 }, (_, i) => end - (1439 - i) * minute)
  const axisStart = end - 24 * 60 * minute

  const xOf = (t: number) => {
    const span = end - axisStart
    return pad.left + ((t - axisStart) / span) * (width - pad.left - pad.right)
  }
  const labelOf = (t: number) => {
    const d = new Date(t)
    return `${d.getHours()}:${d.getMinutes().toString().padStart(2, '0')}`
  }
  const anchorOf = (t: number) => {
    const x = xOf(t)
    if (x <= pad.left + 8) return 'start' as const
    if (x >= width - pad.right - 8) return 'end' as const
    return 'middle' as const
  }

  it('shows 19:31, 17:31, 15:31 and so on across a 24-hour desktop axis', () => {
    const ticks = axisTicksForTimestamps(timestamps, 14)
    const descendingLabels = [...ticks].reverse().slice(0, 4).map(labelOf)
    expect(descendingLabels).toEqual(['19:31', '17:31', '15:31', '13:31'])

    const gaps = ticks.slice(1).map((t, i) => t - ticks[i])
    expect(new Set(gaps).size).toBe(1)
    expect(gaps[0]).toBe(2 * 60 * minute)
  })

  it('places those labels at exactly equal x-axis intervals', () => {
    const ticks = axisTicksForTimestamps(timestamps, 14)
    const positions = ticks.map(xOf)
    const pixelGaps = positions.slice(1).map((x, i) => (x - positions[i]).toFixed(6))
    expect(new Set(pixelGaps).size).toBe(1)
  })

  it('anchors the newest label exactly on the newest sample', () => {
    const ticks = axisTicksForTimestamps(timestamps, 14)
    expect(ticks[ticks.length - 1]).toBe(timestamps[timestamps.length - 1])
  })

  it('recovers the nominal 24-hour start from 1440 one-minute samples', () => {
    const ticks = axisTicksForTimestamps(timestamps, 14)
    expect(ticks[0]).toBe(end - 24 * 60 * minute)
    expect(labelOf(ticks[0])).toBe('19:31')
  })

  it('needs no collision pruning on the anchored 24-hour sequence', () => {
    const ticks = axisTicksForTimestamps(timestamps, 14)
    const kept = pruneCollidingAxisTicks(ticks, xOf, labelOf, charWidth, anchorOf)
    expect(kept).toEqual(ticks)

    const extentOf = (t: number) => {
      const centre = xOf(t)
      const w = labelOf(t).length * charWidth
      const anchor = anchorOf(t)
      if (anchor === 'start') return { left: centre, right: centre + w }
      if (anchor === 'end') return { left: centre - w, right: centre }
      return { left: centre - w / 2, right: centre + w / 2 }
    }
    const extents = kept.map(extentOf)
    for (let i = 1; i < extents.length; i += 1) {
      expect(extents[i].left).toBeGreaterThanOrEqual(extents[i - 1].right)
    }
  })
})
