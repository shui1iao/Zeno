// Pure geometry and scale helpers for the server-detail resource charts.
// Kept free of React so that the drawing rules can be unit-tested directly.

export type ResourceScale = 'percent' | 'rate' | 'count'

export interface ResourceDomain {
  max: number
  /** Grid values from top to bottom: max, 3/4, 1/2, 1/4, 0. */
  ticks: [number, number, number, number, number]
}

export interface TimedValue {
  t: number
  v: number
}

const kib = 1024
/** Smallest 1-2-2.5-5 step (times a power of ten) not below value. */
export function niceStep(value: number, allowQuarter = true): number {
  if (!(value > 0)) return 1
  const magnitude = 10 ** Math.floor(Math.log10(value))
  const factors = allowQuarter ? [1, 2, 2.5, 5, 10] : [1, 2, 5, 10]
  for (const factor of factors) {
    const candidate = Number((factor * magnitude).toPrecision(12))
    if (candidate >= value) return candidate
  }
  return 10 * magnitude
}

export function maxFinite(values: ReadonlyArray<number | null>): number | null {
  let max: number | null = null
  for (const value of values) {
    if (typeof value === 'number' && Number.isFinite(value) && (max === null || value > max)) max = value
  }
  return max
}

/**
 * Four equal divisions, like the latency chart: percentages are fixed to
 * 0-100; rates step in 1024-based units so labels read 1.5 MB/s, 256 KB/s;
 * counts step in whole numbers.
 */
export function resourceDomain(values: ReadonlyArray<number | null>, scale: ResourceScale): ResourceDomain {
  let step: number
  if (scale === 'percent') {
    step = 25
  } else {
    // Leave a little headroom so a flat series does not sit on the top border.
    const target = (maxFinite(values) ?? 0) * 1.08
    if (scale === 'rate') {
      let base = 1
      while (target / base >= 1000 && base < kib ** 4) base *= kib
      if (target <= 0) base = kib
      step = niceStep(Math.max(target, base) / base / 4) * base
    } else {
      step = Math.max(1, Math.ceil(niceStep(Math.max(target, 4) / 4, false)))
    }
  }
  const max = step * 4
  return { max, ticks: [max, step * 3, step * 2, step, 0] }
}

function trimNumber(value: number): string {
  return String(Number(value.toFixed(2)))
}

/** Every label on one axis uses the unit of the top label: 1.6 MB/s, 0.8 MB/s, 0. */
export function formatResourceAxis(value: number, scale: ResourceScale, domainMax: number): string {
  if (scale === 'percent') return `${Math.round(value)}%`
  if (scale === 'count') return trimNumber(value)
  if (value <= 0) return '0'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s']
  let base = 1
  let unit = 0
  while (domainMax / base >= kib && unit < units.length - 1) {
    base *= kib
    unit += 1
  }
  return `${trimNumber(value / base)} ${units[unit]}`
}

/** Median spacing between samples; robust against a few missing buckets. */
export function sampleCadenceMs(timestamps: ReadonlyArray<number>): number {
  const gaps: number[] = []
  for (let index = 1; index < timestamps.length; index += 1) {
    const gap = timestamps[index] - timestamps[index - 1]
    if (gap > 0 && Number.isFinite(gap)) gaps.push(gap)
  }
  if (gaps.length === 0) return 0
  gaps.sort((left, right) => left - right)
  return gaps[Math.floor(gaps.length / 2)]
}

/**
 * Splits a series into drawable runs. A run ends at a missing value or where
 * consecutive samples are further apart than gapMs, so offline periods show as
 * breaks instead of a straight line bridging the hole.
 */
export function splitSegments(timestamps: ReadonlyArray<number>, values: ReadonlyArray<number | null>, gapMs: number): TimedValue[][] {
  const segments: TimedValue[][] = []
  let current: TimedValue[] = []
  let previousT: number | null = null
  for (let index = 0; index < timestamps.length; index += 1) {
    const t = timestamps[index]
    const v = values[index]
    const usable = Number.isFinite(t) && typeof v === 'number' && Number.isFinite(v)
    if (!usable) {
      if (current.length > 0) segments.push(current)
      current = []
      previousT = null
      continue
    }
    if (previousT !== null && gapMs > 0 && t - previousT > gapMs && current.length > 0) {
      segments.push(current)
      current = []
    }
    current.push({ t, v: v as number })
    previousT = t
  }
  if (current.length > 0) segments.push(current)
  return segments
}

/**
 * Largest-Triangle-Three-Buckets downsampling. Keeps real samples (no
 * averaging), so short CPU or traffic spikes survive while a 2880-sample day
 * collapses to roughly one point per pixel.
 */
export function downsampleLttb(points: ReadonlyArray<TimedValue>, threshold: number): TimedValue[] {
  const count = points.length
  if (threshold >= count || threshold < 3) return points.slice()
  const sampled: TimedValue[] = [points[0]]
  const bucketSize = (count - 2) / (threshold - 2)
  let anchor = 0
  for (let bucket = 0; bucket < threshold - 2; bucket += 1) {
    const nextStart = Math.floor((bucket + 1) * bucketSize) + 1
    const nextEnd = Math.min(Math.floor((bucket + 2) * bucketSize) + 1, count)
    let avgT = 0
    let avgV = 0
    const nextLength = Math.max(1, nextEnd - nextStart)
    for (let index = nextStart; index < nextEnd; index += 1) {
      avgT += points[index].t
      avgV += points[index].v
    }
    avgT /= nextLength
    avgV /= nextLength

    const rangeStart = Math.floor(bucket * bucketSize) + 1
    const rangeEnd = Math.floor((bucket + 1) * bucketSize) + 1
    const a = points[anchor]
    let maxArea = -1
    let chosen = rangeStart
    for (let index = rangeStart; index < rangeEnd; index += 1) {
      const area = Math.abs((a.t - avgT) * (points[index].v - a.v) - (a.t - points[index].t) * (avgV - a.v))
      if (area > maxArea) {
        maxArea = area
        chosen = index
      }
    }
    sampled.push(points[chosen])
    anchor = chosen
  }
  sampled.push(points[count - 1])
  return sampled
}

/** Downsamples every run in proportion to its length so the total stays near budget. */
export function downsampleSegments(segments: TimedValue[][], budget: number): TimedValue[][] {
  const total = segments.reduce((sum, segment) => sum + segment.length, 0)
  if (total <= budget) return segments
  return segments.map((segment) => downsampleLttb(segment, Math.max(3, Math.round((segment.length / total) * budget))))
}

/** Index of the sample closest to t; timestamps must be ascending. */
export function nearestIndex(timestamps: ReadonlyArray<number>, t: number): number {
  if (timestamps.length === 0) return -1
  let low = 0
  let high = timestamps.length - 1
  while (low < high) {
    const middle = (low + high) >> 1
    if (timestamps[middle] < t) low = middle + 1
    else high = middle
  }
  if (low > 0 && Math.abs(timestamps[low - 1] - t) <= Math.abs(timestamps[low] - t)) return low - 1
  return low
}

export function lastFiniteIndex(values: ReadonlyArray<number | null>): number {
  for (let index = values.length - 1; index >= 0; index -= 1) {
    const value = values[index]
    if (typeof value === 'number' && Number.isFinite(value)) return index
  }
  return -1
}
