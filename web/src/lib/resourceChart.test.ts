import { describe, expect, it } from 'vitest'
import {
  downsampleLttb,
  formatResourceAxis,
  nearestIndex,
  niceStep,
  resourceDomain,
  sampleCadenceMs,
  splitSegments,
} from './resourceChart'

describe('resource chart scales', () => {
  it('picks 1-2-2.5-5 steps', () => {
    expect(niceStep(15.5)).toBe(20)
    expect(niceStep(0.22)).toBe(0.25)
    expect(niceStep(2.2, false)).toBe(5)
  })

  it('keeps percentages on a fixed 0-100 axis in four steps', () => {
    expect(resourceDomain([1, 2, 3], 'percent').ticks).toEqual([100, 75, 50, 25, 0])
  })

  it('labels every rate tick in the unit of the top tick', () => {
    const domain = resourceDomain([60 * 1024 * 1024], 'rate')
    expect(domain.ticks.map((tick) => formatResourceAxis(tick, 'rate', domain.max))).toEqual(['80 MB/s', '60 MB/s', '40 MB/s', '20 MB/s', '0'])
    const small = resourceDomain([0, 0], 'rate')
    expect(small.ticks.map((tick) => formatResourceAxis(tick, 'rate', small.max))).toEqual(['1 KB/s', '0.75 KB/s', '0.5 KB/s', '0.25 KB/s', '0'])
  })

  it('keeps count ticks whole and survives huge payloads', () => {
    expect(resourceDomain([156], 'count').ticks).toEqual([200, 150, 100, 50, 0])
    expect(resourceDomain([0], 'count').max).toBe(4)
    const values = Array.from({ length: 250_000 }, (_, index) => index % 100)
    expect(() => resourceDomain(values, 'count')).not.toThrow()
  })
})

describe('resource chart geometry', () => {
  it('splits on missing values and on gaps wider than the cadence allowance', () => {
    const t = [0, 30, 60, 90, 600, 630]
    expect(sampleCadenceMs(t)).toBe(30)
    const segments = splitSegments(t, [1, 2, null, 4, 5, 6], 90)
    expect(segments.map((segment) => segment.map((point) => point.v))).toEqual([[1, 2], [4], [5, 6]])
  })

  it('keeps spikes when downsampling', () => {
    const points = Array.from({ length: 2880 }, (_, index) => ({ t: index * 30_000, v: index === 1500 ? 95 : 5 }))
    const sampled = downsampleLttb(points, 300)
    expect(sampled.length).toBe(300)
    expect(sampled.some((point) => point.v === 95)).toBe(true)
    expect(sampled[0]).toBe(points[0])
    expect(sampled.at(-1)).toBe(points.at(-1))
  })

  it('finds the nearest sample', () => {
    expect(nearestIndex([0, 10, 20], 14)).toBe(1)
    expect(nearestIndex([0, 10, 20], 16)).toBe(2)
    expect(nearestIndex([], 5)).toBe(-1)
  })
})
