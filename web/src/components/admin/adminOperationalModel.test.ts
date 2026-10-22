import { describe, expect, it } from 'vitest'
import { formatRenewalDayOption, normalizeRenewalDays, normalizeRenewalThreshold, parseRenewalThreshold, renewalDayOptions } from './adminOperationalModel'

describe('renewal reminder options', () => {
  it('removes same-day reminders and preserves supported lead times', () => {
    expect(renewalDayOptions).toEqual([1, 3, 7, 15, 30])
    expect(parseRenewalThreshold('0')).toBeNull()
    expect(normalizeRenewalThreshold(0)).toBe(1)
  })

  it('labels the calendar-month option separately from day durations', () => {
    expect(formatRenewalDayOption(1)).toBe('提前 1 天')
    expect(formatRenewalDayOption(15)).toBe('提前半个月')
    expect(formatRenewalDayOption(30)).toBe('提前 1 个月')
  })

  it('normalizes multiple reminder days without duplicates and keeps the legacy threshold fallback', () => {
    expect(normalizeRenewalDays([7, 1, 7, 2], 3)).toEqual([1, 7])
    expect(normalizeRenewalDays([], 15)).toEqual([15])
  })
})
