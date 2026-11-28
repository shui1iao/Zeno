import type { NodeStatus } from '../types'

export interface ExpiryBadge {
  text: string
  /** Remaining whole days; null for permanent or unparseable labels. */
  days: number | null
}

/** Interprets the controller's precomputed expiry label ("余 N 天", a date, 永久…). */
export function expiryBadge(expiryLabel: string | null | undefined, now: Date = new Date()): ExpiryBadge | null {
  const trimmed = (expiryLabel ?? '').trim()
  if (trimmed === '') return null
  if (trimmed === '已过期') return { text: trimmed, days: -1 }
  if (trimmed === '今天到期') return { text: '余 0 天', days: 0 }
  const relativeMatch = /^余\s*(\d+)\s*天$/.exec(trimmed)
  if (relativeMatch) {
    const days = Number(relativeMatch[1])
    return { text: `余 ${days} 天`, days }
  }
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(trimmed)
  if (!match) return { text: trimmed === '永 久' ? '永久' : trimmed, days: null }
  const expiry = Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3]))
  const today = Date.UTC(now.getFullYear(), now.getMonth(), now.getDate())
  const days = Math.ceil((expiry - today) / 86400000)
  if (days < 0) return { text: '已过期', days }
  return { text: `余 ${days} 天`, days }
}

export function isOfflineStatus(status: NodeStatus | null | undefined): boolean {
  return status === 'offline' || status === 'no_data'
}

export type NodeVisualStatus = 'online' | 'warning' | 'offline'

export const nodeStatusLabels: Record<NodeVisualStatus, string> = { online: '在线', warning: '异常', offline: '离线' }

/** Home card / list / status matrix tone: offline wins, then warning, everything else reads as online. */
export function nodeVisualStatus(status: NodeStatus | null | undefined): NodeVisualStatus {
  return isOfflineStatus(status) ? 'offline' : status === 'warning' ? 'warning' : 'online'
}
