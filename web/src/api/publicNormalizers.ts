import type { AdminSettings, HomeCardNode, LatencyPoint, ServiceTarget, StatePoint } from '../types'
import { normalizeCurrencyRates } from '../lib/currency'
import type { ApiLatencyPoint, ApiLatencyResponse, ApiLatencySeries, ApiLatencySummary, ApiNode, ApiServiceLatencyPoint, ApiServiceLatencyResponse, ApiServiceLatencySeries, ApiServiceTarget, ApiSettings, ApiStatePoint, ApiStateResponse, NodeLatencyData, NodeStateData, ServiceLatencyData, SummaryData, ApiSummaryResponse } from './apiTypes'

export function normalizeSettings(input: ApiSettings): AdminSettings {
  const logoUrl = input.logo_url
  const desktopBackgroundUrl = input.desktop_background_url ?? input.background_url
  return {
    siteTitle: input.site_title,
    logoUrl,
    theme: input.theme ?? 'system',
    agentControllerUrl: input.agent_controller_url ?? '',
    backgroundUrl: desktopBackgroundUrl,
    desktopBackgroundUrl,
    mobileBackgroundUrl: input.mobile_background_url ?? '',
    appearancePreset: input.appearance_preset ?? 'default',
    serverCardTheme: input.server_card_theme === 'capsule' ? 'capsule' : 'classic',
    cardOpacity: input.card_opacity ?? 0.7,
    cardBlur: input.card_blur ?? 0,
    cardRadius: input.card_radius ?? 20,
    borderStrength: input.border_strength ?? 0.3,
    shadowStrength: input.shadow_strength ?? 0.2,
    backgroundOverlay: input.background_overlay ?? 0,
    themeColor: input.theme_color ?? '#2563eb',
    customCode: input.custom_code ?? '',
    revision: Number.isSafeInteger(input.revision) && (input.revision ?? -1) >= 0 ? input.revision! : 0,
    updatedAt: input.updated_at,
  }
}

export function normalizeSummary(input: ApiSummaryResponse): SummaryData {
  return {
    nodes: (input.nodes ?? []).map(normalizeNode),
    services: (input.services ?? []).map(normalizeServiceTarget),
    latencyPoints: (input.latency_points ?? []).map(normalizeLatencyPoint),
    exchangeRates: normalizeCurrencyRates(input.exchange_rates),
  }
}

export function normalizeNodeLatency(input: ApiLatencyResponse): NodeLatencyData {
  return {
    nodeId: input.node_id,
    range: input.range,
    points: normalizeNodeLatencyPoints(input),
    snapshotKey: nodeLatencySnapshotKey(input),
  }
}

type SnapshotHash = { first: number; second: number }

const snapshotNumberBuffer = new ArrayBuffer(8)
const snapshotNumberView = new DataView(snapshotNumberBuffer)

function mixSnapshotHash(hash: SnapshotHash, value: number): void {
  const part = value | 0
  hash.first = Math.imul(hash.first ^ part, 16_777_619)
  hash.second = Math.imul(hash.second ^ part, 2_246_822_519)
}

function mixSnapshotString(hash: SnapshotHash, value: string): void {
  mixSnapshotHash(hash, value.length)
  for (let index = 0; index < value.length; index += 1) mixSnapshotHash(hash, value.charCodeAt(index))
}

function mixSnapshotNumber(hash: SnapshotHash, value: number | null | undefined): void {
  if (value === null || value === undefined) {
    mixSnapshotHash(hash, 0x6d2b79f5)
    return
  }
  if (!Number.isFinite(value)) {
    mixSnapshotHash(hash, 0x1b873593)
    return
  }
  snapshotNumberView.setFloat64(0, value === 0 ? 0 : value)
  mixSnapshotHash(hash, snapshotNumberView.getUint32(0))
  mixSnapshotHash(hash, snapshotNumberView.getUint32(4))
}

function mixSnapshotNumbers(hash: SnapshotHash, values: Array<number | null> | null | undefined): void {
  mixSnapshotHash(hash, values?.length ?? 0)
  for (const value of values ?? []) mixSnapshotNumber(hash, value)
}

export function nodeLatencySnapshotKey(input: ApiLatencyResponse): string {
  const hash: SnapshotHash = { first: 0x811c9dc5, second: 0x9e3779b9 }
  mixSnapshotString(hash, input.node_id)
  mixSnapshotString(hash, input.range)
  if (input.points) {
    for (const point of input.points) {
      mixSnapshotString(hash, point.ts)
      mixSnapshotString(hash, point.target_id)
      mixSnapshotString(hash, point.target_name)
      mixSnapshotNumber(hash, point.median_ms)
      mixSnapshotNumber(hash, point.avg_ms)
      mixSnapshotNumber(hash, point.loss_percent)
    }
  } else {
    mixSnapshotNumbers(hash, input.created_at)
    for (const series of input.series ?? []) {
      mixSnapshotString(hash, series.target_id)
      mixSnapshotString(hash, series.target_name)
      mixSnapshotNumbers(hash, series.created_at)
      mixSnapshotNumbers(hash, series.median_ms)
      mixSnapshotNumbers(hash, series.avg_ms)
      mixSnapshotNumbers(hash, series.loss_percent)
    }
  }
  return `${(hash.first >>> 0).toString(36)}:${(hash.second >>> 0).toString(36)}`
}

export function normalizeServiceLatency(input: ApiServiceLatencyResponse): ServiceLatencyData {
  return {
    target: normalizeServiceTarget(input.target),
    range: input.range,
    points: normalizeServiceLatencyPoints(input),
  }
}

export function normalizeNodeState(input: ApiStateResponse): NodeStateData {
  return {
    nodeId: input.node_id,
    range: input.range,
    points: normalizeNodeStatePoints(input),
  }
}

export function normalizeNode(node: ApiNode): HomeCardNode {
  return {
    id: node.id,
    displayName: node.display_name,
    status: node.status,
    os: node.os,
    osVersion: node.os_version,
    kernel: node.kernel,
    arch: node.arch,
    virtualization: node.virtualization,
    cpuModel: node.cpu_model,
    countryCode: node.country_code,
    subtitle: node.subtitle,
    cpuCores: node.cpu_cores ?? null,
    expiryLabel: node.expiry_label,
    renewalAmount: node.renewal_amount ?? null,
    renewalCurrency: node.renewal_currency,
    billingCycle: node.billing_cycle,
    monthlyCostCny: node.monthly_cost_cny ?? null,
    cpuPercent: node.cpu_percent,
    memoryUsedBytes: node.memory_used_bytes,
    memoryTotalBytes: node.memory_total_bytes,
    diskUsedBytes: node.disk_used_bytes,
    diskTotalBytes: node.disk_total_bytes,
    bootTime: node.boot_time ?? undefined,
    load1: node.load1 ?? null,
    load5: node.load5 ?? null,
    load15: node.load15 ?? null,
    uptimeSeconds: node.uptime_seconds ?? null,
    netInSpeedBps: node.net_in_speed_bps,
    netOutSpeedBps: node.net_out_speed_bps,
    netInTotalBytes: node.net_in_total_bytes,
    netOutTotalBytes: node.net_out_total_bytes,
    netInLifetimeBytes: node.net_in_lifetime_bytes,
    netOutLifetimeBytes: node.net_out_lifetime_bytes,
    billingMode: node.billing_mode,
    monthlyResetDay: node.monthly_reset_day,
    monthlyPeriodStart: node.monthly_period_start,
    monthlyPeriodEnd: node.monthly_period_end,
    monthlyBillableBytes: node.monthly_billable_bytes,
    monthlyQuotaBytes: node.monthly_quota_bytes,
    latencySummary: node.latency_summary ? normalizeLatencySummary(node.latency_summary) : undefined,
    latencySummaries: (node.latency_summaries ?? []).map(normalizeLatencySummary),
  }
}

export function normalizeLatencySummary(summary: ApiLatencySummary) {
  return {
    targetId: summary.target_id,
    targetName: summary.target_name,
    medianMs: summary.median_ms,
    avgMs: summary.avg_ms ?? null,
    lossPercent: summary.loss_percent,
    updatedAt: summary.updated_at,
    hourlyHistory: (summary.hourly_history ?? []).map((point) => ({
      startedAt: point.started_at,
      latencyMs: point.latency_ms,
      lossPercent: point.loss_percent,
    })),
  }
}

export function normalizeLatencyPoint(point: ApiLatencyPoint): LatencyPoint {
  const parsedTimestamp = Date.parse(point.ts)
  return {
    ts: point.ts,
    tsMs: Number.isFinite(parsedTimestamp) ? parsedTimestamp : 0,
    targetId: point.target_id,
    targetName: point.target_name,
    medianMs: point.median_ms,
    avgMs: point.avg_ms ?? null,
    lossPercent: point.loss_percent,
  }
}

export function normalizeNodeLatencyPoints(input: ApiLatencyResponse): LatencyPoint[] {
  if (input.points) return input.points.map(normalizeLatencyPoint)
  return normalizeLatencySeries(input.series, input.created_at ?? [], (series) => ({
    targetId: series.target_id,
    targetName: series.target_name,
  }))
}

type ApiLatencySeriesValues = Pick<ApiLatencySeries, 'created_at' | 'median_ms' | 'avg_ms' | 'loss_percent'>

function normalizeLatencySeries<T extends ApiLatencySeriesValues>(
  seriesList: T[] | null | undefined,
  sharedCreatedAt: number[],
  identity: (series: T) => Pick<LatencyPoint, 'targetId' | 'targetName'>,
): LatencyPoint[] {
  return (seriesList ?? []).flatMap((series) => {
    const medianValues = series.median_ms ?? []
    const avgValues = series.avg_ms ?? []
    const lossValues = series.loss_percent ?? []
    const target = identity(series)
    return (series.created_at ?? sharedCreatedAt).map((createdAt, index) => {
      const medianMs = medianValues[index] ?? null
      const tsMs = normalizeSeriesTimestampValue(createdAt)
      return {
        ts: new Date(tsMs).toISOString(),
        tsMs,
        ...target,
        medianMs,
        avgMs: avgValues[index] ?? null,
        lossPercent: lossValues[index] ?? 0,
      }
    })
  })
}

export function normalizeServiceLatencyPoints(input: ApiServiceLatencyResponse): LatencyPoint[] {
  if (input.points) return input.points.map(normalizeServiceLatencyPoint)
  return normalizeLatencySeries<ApiServiceLatencySeries>(input.series, input.created_at ?? [], (series) => ({
    targetId: series.node_id,
    targetName: series.node_name,
  }))
}

function normalizeSeriesTimestampValue(value: number): number {
  const timestamp = new Date(value).getTime()
  return Number.isNaN(timestamp) ? 0 : timestamp
}

export function normalizeSeriesTimestamp(value: number): string {
  return new Date(normalizeSeriesTimestampValue(value)).toISOString()
}

export function normalizeServiceTarget(target: ApiServiceTarget): ServiceTarget {
  return {
    id: target.id,
    name: target.name,
    type: target.type,
    assignedNodeCount: target.assigned_node_count,
    reportingNodeCount: target.reporting_node_count,
    medianMs: target.median_ms,
    avgMs: target.avg_ms ?? null,
    lossPercent: target.loss_percent,
    updatedAt: target.updated_at,
  }
}

export function normalizeServiceLatencyPoint(point: ApiServiceLatencyPoint): LatencyPoint {
  return {
    ts: point.ts,
    targetId: point.node_id,
    targetName: point.node_name,
    medianMs: point.median_ms,
    avgMs: point.avg_ms ?? null,
    lossPercent: point.loss_percent,
  }
}

export function normalizeStatePoint(point: ApiStatePoint): StatePoint {
  return {
    ts: point.ts,
    cpuPercent: point.cpu_percent,
    load1: point.load1 ?? null,
    load5: point.load5 ?? null,
    load15: point.load15 ?? null,
    memoryUsedBytes: point.memory_used_bytes,
    memoryTotalBytes: point.memory_total_bytes,
    swapUsedBytes: point.swap_used_bytes ?? null,
    swapTotalBytes: point.swap_total_bytes ?? null,
    diskUsedBytes: point.disk_used_bytes,
    diskTotalBytes: point.disk_total_bytes,
    netInTotalBytes: point.net_in_total_bytes,
    netOutTotalBytes: point.net_out_total_bytes,
    netInSpeedBps: point.net_in_speed_bps,
    netOutSpeedBps: point.net_out_speed_bps,
    processCount: point.process_count ?? null,
    tcpConnectionCount: point.tcp_connection_count ?? null,
    udpConnectionCount: point.udp_connection_count ?? null,
    uptimeSeconds: point.uptime_seconds,
  }
}

export function normalizeNodeStatePoints(input: ApiStateResponse): StatePoint[] {
  if (input.points) return input.points.map(normalizeStatePoint)
  const timestamps = input.created_at ?? []
  const series = input.series ?? {}
  return timestamps.map((createdAt, index) => ({
    ts: normalizeSeriesTimestamp(createdAt),
    cpuPercent: stateSeriesValue(series.cpu_percent, index),
    load1: stateSeriesValue(series.load1, index),
    load5: stateSeriesValue(series.load5, index),
    load15: stateSeriesValue(series.load15, index),
    memoryUsedBytes: stateSeriesValue(series.memory_used_bytes, index),
    memoryTotalBytes: stateSeriesValue(series.memory_total_bytes, index),
    swapUsedBytes: stateSeriesValue(series.swap_used_bytes, index),
    swapTotalBytes: stateSeriesValue(series.swap_total_bytes, index),
    diskUsedBytes: stateSeriesValue(series.disk_used_bytes, index),
    diskTotalBytes: stateSeriesValue(series.disk_total_bytes, index),
    netInTotalBytes: stateSeriesValue(series.net_in_total_bytes, index),
    netOutTotalBytes: stateSeriesValue(series.net_out_total_bytes, index),
    netInSpeedBps: stateSeriesValue(series.net_in_speed_bps, index),
    netOutSpeedBps: stateSeriesValue(series.net_out_speed_bps, index),
    processCount: stateSeriesValue(series.process_count, index),
    tcpConnectionCount: stateSeriesValue(series.tcp_connection_count, index),
    udpConnectionCount: stateSeriesValue(series.udp_connection_count, index),
    uptimeSeconds: stateSeriesValue(series.uptime_seconds, index),
  }))
}

export function stateSeriesValue(values: Array<number | null> | null | undefined, index: number): number | null {
  if (!values || index < 0 || index >= values.length) return null
  const value = values[index]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}
