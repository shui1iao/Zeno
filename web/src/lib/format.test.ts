import { describe, expect, it } from 'vitest'
import { formatBytes, formatBps, formatClock, formatLatency, formatPercent, formatTooltipDateTime } from './format'

describe('formatBytes', () => {
  it('formats nullish values as the shared missing marker', () => {
    expect(formatBytes(null)).toBe('--')
    expect(formatBytes(undefined)).toBe('--')
  })

  it('formats 1024-based units with the GB/KB spelling used by the home cards', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(1073741824)).toBe('1.0 GB')
  })
})

describe('formatBps', () => {
  it('formats network speeds per second', () => {
    expect(formatBps(2048)).toBe('2.0 KB/s')
    expect(formatBps(306278)).toBe('299.1 KB/s')
  })
})

describe('formatPercent', () => {
  it('formats percentages with one decimal place', () => {
    expect(formatPercent(12.345)).toBe('12.3%')
  })

  it('uses the missing marker without a unit for nullish values', () => {
    expect(formatPercent(null)).toBe('--')
  })
})

describe('formatLatency', () => {
  it('keeps one decimal below 100 ms, rounds from 100 ms and spaces the unit', () => {
    expect(formatLatency(33.14)).toBe('33.1 ms')
    expect(formatLatency(100.4)).toBe('100 ms')
    expect(formatLatency(null)).toBe('--')
  })
})

describe('time formatting', () => {
  it('pads hours to HH:mm and shares one tooltip format', () => {
    const date = new Date(2026, 6, 2, 9, 5)
    expect(formatClock(date)).toBe('09:05')
    expect(formatTooltipDateTime(date)).toBe('07/02 09:05')
  })
})
