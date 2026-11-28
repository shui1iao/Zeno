// Shared display formatting. Byte units use the 1024-based GB/KB spelling that
// the home cards already use, and every number keeps one space before its unit.
export const missingValue = '--'

export const byteUnits = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'] as const

/**
 * Divides a byte magnitude by 1024 until it drops below 1024 or reaches
 * byteUnits[maxPower]. Callers keep their own rounding, sign and empty rules.
 */
export function scaleBytes(magnitude: number, maxPower = byteUnits.length - 1): { size: number; power: number } {
  let size = magnitude
  let power = 0
  while (size >= 1024 && power < maxPower) {
    size /= 1024
    power += 1
  }
  return { size, power }
}

export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return missingValue
  if (value === 0) return '0 B'

  const { size, power } = scaleBytes(Math.abs(value))
  const signed = value < 0 ? -size : size
  if (power === 0) return `${Math.round(signed)} ${byteUnits[power]}`
  return `${signed.toFixed(1)} ${byteUnits[power]}`
}

export function formatBps(value: number | null | undefined): string {
  const formatted = formatBytes(value)
  return formatted === missingValue ? formatted : `${formatted}/s`
}

export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return missingValue
  return `${value.toFixed(1)}%`
}

export function formatLatency(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return missingValue
  return `${value.toFixed(value >= 100 ? 0 : 1)} ms`
}

function pad2(value: number): string {
  return value.toString().padStart(2, '0')
}

/** Zero-padded local calendar date, e.g. 2026-07-29 or 2026/07/29. */
export function formatCalendarDate(date: Date, separator = '-'): string {
  return `${date.getFullYear()}${separator}${pad2(date.getMonth() + 1)}${separator}${pad2(date.getDate())}`
}

/** HH:mm with a zero-padded hour. */
export function formatClock(timestamp: number | Date): string {
  const date = timestamp instanceof Date ? timestamp : new Date(timestamp)
  if (Number.isNaN(date.getTime())) return '--:--'
  return `${pad2(date.getHours())}:${pad2(date.getMinutes())}`
}

/** MM/DD HH:mm, shared by every chart readout tooltip. */
export function formatTooltipDateTime(timestamp: number | Date): string {
  const date = timestamp instanceof Date ? timestamp : new Date(timestamp)
  if (Number.isNaN(date.getTime())) return missingValue
  return `${pad2(date.getMonth() + 1)}/${pad2(date.getDate())} ${formatClock(date)}`
}

/** YYYY/MM/DD HH:mm for absolute timestamps such as boot or update times. */
export function formatDateTime(value: string | number | Date | null | undefined): string {
  if (value === null || value === undefined || value === '') return missingValue
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) return typeof value === 'string' ? value : missingValue
  return `${formatCalendarDate(date, '/')} ${formatClock(date)}`
}
