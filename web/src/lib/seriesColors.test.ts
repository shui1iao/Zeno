import { describe, expect, it } from 'vitest'
import { latencySeriesColor, seriesColorMap } from './seriesColors'

function hslLightnessAndSaturation(hex: string): { lightness: number; saturation: number } {
  const [r, g, b] = [1, 3, 5].map((offset) => parseInt(hex.slice(offset, offset + 2), 16) / 255)
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const lightness = (max + min) / 2
  const saturation = max === min ? 0 : (max - min) / (1 - Math.abs(2 * lightness - 1))
  return { lightness, saturation }
}

describe('latency series colours', () => {
  it('produces the first colour from the golden-angle generator too (no fixed palette)', () => {
    const { lightness, saturation } = hslLightnessAndSaturation(latencySeriesColor(0))
    expect(latencySeriesColor(0)).toMatch(/^#[0-9a-f]{6}$/)
    expect(lightness).toBeGreaterThanOrEqual(0.55)
    expect(saturation).toBeGreaterThanOrEqual(0.7)
    expect(['#22c55e', '#38bdf8', '#f59e0b']).not.toContain(latencySeriesColor(0))
  })

  it.each([1, 2, 10, 11, 19, 50, 200])('gives %i series pairwise distinct colours', (count) => {
    const colors = Array.from({ length: count }, (_, index) => latencySeriesColor(index))
    expect(new Set(colors).size).toBe(count)
    colors.forEach((color) => expect(color).toMatch(/^#[0-9a-f]{6}$/))
  })

  it('keeps every colour from 1 to 200 distinct', () => {
    for (let count = 1; count <= 200; count += 1) {
      const colors = Array.from({ length: count }, (_, index) => latencySeriesColor(index))
      expect(new Set(colors).size).toBe(count)
    }
  })

  it('only generates bright colours (never darkened)', () => {
    for (let index = 0; index < 200; index += 1) {
      const { lightness, saturation } = hslLightnessAndSaturation(latencySeriesColor(index))
      expect(lightness).toBeGreaterThanOrEqual(0.55)
      expect(saturation).toBeGreaterThanOrEqual(0.7)
    }
  })

  it('assigns colours by the global order so a target keeps its colour on every page', () => {
    const globalOrder = ['a', 'b', 'c', 'd']
    const firstNode = seriesColorMap(globalOrder, ['c', 'a'])
    const secondNode = seriesColorMap(globalOrder, ['d', 'c'])
    expect(firstNode.get('c')).toBe(secondNode.get('c'))
    expect(firstNode.get('c')).toBe(latencySeriesColor(2))
    expect(seriesColorMap(['a'], ['zz']).get('zz')).toBe(latencySeriesColor(1))
  })
})
