import { currencyOptions as displayCurrencyOptions } from '../../lib/currency'
import type { AdminAlertRule, AdminNode, AdminProbeTarget, ProbeType } from '../../types'

export const renewalDayOptions = [1, 3, 7, 15, 30]

export const billingModeOptions = [
  { value: 'both', label: '双向' },
  { value: 'in', label: '入站' },
  { value: 'out', label: '出站' },
  { value: 'max', label: '出入取大' },
]

export const billingCycleOptions = [
  { value: '月', label: '月' },
  { value: '季', label: '季' },
  { value: '半年', label: '半年' },
  { value: '年', label: '年' },
  { value: '两年', label: '两年' },
  { value: '三年', label: '三年' },
  { value: '五年', label: '五年' },
]

export const renewalCurrencyOptions = displayCurrencyOptions
  .filter(({ value }) => value !== 'SGD' && value !== 'KRW')
  .map(({ value, label }) => ({ value, label }))

export const quotaUnitOptions = [
  { value: 'GB', label: 'GB' },
  { value: 'TB', label: 'TB' },
]

export const targetTypeOptions = [
  { value: 'tcping', label: 'TCP Ping' },
  { value: 'ping', label: 'ICMP Ping' },
  { value: 'http_get', label: 'HTTP GET' },
]

export function targetAssignmentRows(target: AdminProbeTarget, nodes: AdminNode[]) {
  const assignmentByNodeID = new Map(target.assignments.map((assignment) => [assignment.nodeId, assignment]))
  const nodeAssignmentRows = nodes.map((node) => {
    const assignment = assignmentByNodeID.get(node.id)
    return {
      nodeId: node.id,
      nodeDisplayName: node.displayName,
      enabled: assignment?.enabled ?? false,
    }
  })
  const staleAssignmentRows = target.assignments
    .filter((assignment) => !nodes.some((node) => node.id === assignment.nodeId))
    .map((assignment) => ({
      nodeId: assignment.nodeId,
      nodeDisplayName: assignment.nodeDisplayName || assignment.nodeId,
      enabled: assignment.enabled,
    }))
  return [...nodeAssignmentRows, ...staleAssignmentRows]
}

export function normalizeTargetFormType(value: string): ProbeType {
  if (value === 'ping' || value === 'icmp') return 'ping'
  if (value === 'http_get' || value === 'http' || value === 'https') return 'http_get'
  return 'tcping'
}

export function formatTargetEndpoint(target: AdminProbeTarget): string {
  return target.port ? `${target.address}:${target.port}` : target.address
}

export function formatTargetAssignmentSummary(target: AdminProbeTarget): string {
  if (target.assignments.length === 0) return '未分配服务器'
  const enabled = target.assignments.filter((assignment) => assignment.enabled).length
  return `${enabled} / ${target.assignments.length} 服务器启用`
}

export function formatAlertRuleScope(rule: AdminAlertRule, nodes: AdminNode[]): string {
  if (rule.scopeNodeIds.length === 0) return '全部服务器'
  const labels = rule.scopeNodeIds.map((nodeId) => {
    const node = nodes.find((candidate) => candidate.id === nodeId)
    return node?.displayName || nodeId
  })
  return labels.join('、')
}

export function formatAlertRuleNote(rule: AdminAlertRule): string {
  if (rule.metric === 'expiry_days') return normalizeRenewalDays(rule.renewalDays, rule.threshold).map(formatRenewalDayOption).join('、')
  if (rule.category === 'resource' && rule.thresholdUnit === '%') {
    const windowLabel = rule.durationSec <= 0 ? '当前值' : `${formatDurationCompact(rule.durationSec)}平均`
    return `${windowLabel} ≥ ${formatPercentThreshold(rule.threshold)}%`
  }
  return rule.durationSec <= 0 ? '立即通知' : `${formatDurationCompact(rule.durationSec)}确认`
}

export function formatPercentThreshold(value: number): string {
  if (Number.isInteger(value)) return String(value)
  return String(Math.round(value * 10) / 10)
}

export function formatDurationCompact(seconds: number): string {
  const normalized = Math.max(0, Math.round(seconds))
  if (normalized === 0) return '立即'
  if (normalized % 3600 === 0) return `${normalized / 3600} 小时`
  if (normalized % 60 === 0) return `${normalized / 60} 分钟`
  return `${normalized} 秒`
}

export function formatRenewalDayOption(days: number): string {
  if (days === 15) return '提前半个月'
  if (days === 30) return '提前 1 个月'
  return `提前 ${days} 天`
}

export function parseRenewalThreshold(value: string): number | null {
  const parsed = parseNonNegativeInt(value)
  if (parsed === null || !renewalDayOptions.includes(parsed)) return null
  return parsed
}

export function normalizeRenewalThreshold(value: number): number {
  const normalized = Math.max(0, Math.min(30, Math.round(value)))
  if (normalized <= 1) return 1
  return renewalDayOptions.includes(normalized) ? normalized : 3
}

export function normalizeRenewalDays(values: number[] | undefined, fallback: number): number[] {
  const valid = Array.from(new Set((values ?? []).filter((value) => Number.isInteger(value) && renewalDayOptions.includes(value)))).sort((left, right) => left - right)
  return valid.length > 0 ? valid : [normalizeRenewalThreshold(fallback)]
}

export function parsePositiveInt(value: string): number | null {
  const parsed = Number(value.trim())
  if (!Number.isInteger(parsed) || parsed <= 0) return null
  return parsed
}

export function parseNonNegativeInt(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isInteger(parsed) || parsed < 0) return null
  return parsed
}

export function parsePercentage(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0 || parsed > 100) return null
  return parsed
}

export function parseMonthlyResetDay(value: string): number | null {
  const parsed = parseNonNegativeInt(value)
  if (!parsed || parsed < 1 || parsed > 31) return null
  return parsed
}

export function normalizeBillingCycle(value?: string | null): string {
  const trimmed = (value ?? '').trim()
  if (trimmed.includes('五')) return '五年'
  if (trimmed.includes('三')) return '三年'
  if (trimmed.includes('两') || trimmed.includes('二') || trimmed.includes('2')) return '两年'
  if (trimmed.includes('半')) return '半年'
  if (trimmed.includes('季')) return '季'
  if (trimmed.includes('年')) return '年'
  return '月'
}

export function quotaUnitForBytes(value: number | null): 'GB' | 'TB' {
  if (!value || value < 1024 ** 4) return 'GB'
  return 'TB'
}

export function formatQuotaValue(value: number | null): string {
  if (!value || value <= 0) return ''
  const unit = quotaUnitForBytes(value)
  const divisor = unit === 'TB' ? 1024 ** 4 : 1024 ** 3
  return String(Math.round((value / divisor) * 100) / 100)
}

export function parseQuota(value: string, unit: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0) return null
  const multiplier = unit === 'TB' ? 1024 ** 4 : 1024 ** 3
  return Math.round(parsed * multiplier)
}

export function parseRenewalAmount(value: string): number | null {
  const trimmed = value.trim()
  if (trimmed === '') return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed <= 0) return null
  return Math.round(parsed * 100) / 100
}

export function formatRenewalAmountInput(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value) || value <= 0) return ''
  return String(value)
}
