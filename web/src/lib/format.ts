// Shared display formatting. Byte units use the 1024-based GB/KB spelling that
// the home cards already use, and every number keeps one space before its unit.
export const missingValue = '--'

export function formatBytes(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return missingValue
  if (value === 0) return '0 B'

  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = Math.abs(value)
  let unitIndex = 0
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }

  const signed = value < 0 ? -size : size
  if (unitIndex === 0) return `${Math.round(signed)} ${units[unitIndex]}`
  return `${signed.toFixed(1)} ${units[unitIndex]}`
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
  return `${date.getFullYear()}/${pad2(date.getMonth() + 1)}/${pad2(date.getDate())} ${formatClock(date)}`
}
