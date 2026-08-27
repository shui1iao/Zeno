import type { AdminAlertRule, AdminNode, AdminNotificationChannel, AdminNotificationDelivery, AdminProbeTarget } from '../types'
import type { AdminAlertRuleUpdateInput, AdminAlertRulesData, AdminNodeCreateInput, AdminNodesData, AdminNodeSharedInput, AdminNodeUpdateInput, AdminNotificationChannelCreateInput, AdminNotificationChannelsData, AdminNotificationChannelUpdateInput, AdminProbeTargetInput, AdminProbeTargetSharedInput, AdminProbeTargetsData, AdminProbeTargetUpdateInput, AdminSettingsUpdateInput, ApiAdminAlertRule, ApiAdminAlertRulesResponse, ApiAdminNode, ApiAdminNodesResponse, ApiAdminNotificationChannel, ApiAdminNotificationChannelsResponse, ApiAdminNotificationDelivery, ApiAdminProbeTarget, ApiAdminProbeTargetsResponse } from './apiTypes'

export function normalizeAdminNodes(input: ApiAdminNodesResponse): AdminNodesData {
  return {
    nodes: (input.nodes ?? []).map(normalizeAdminNode),
  }
}

export function normalizeAdminProbeTargets(input: ApiAdminProbeTargetsResponse): AdminProbeTargetsData {
  return {
    targets: input.targets.map(normalizeAdminProbeTarget),
  }
}

export function normalizeAdminNotificationChannels(input: ApiAdminNotificationChannelsResponse): AdminNotificationChannelsData {
  return {
    channels: input.channels.map(normalizeAdminNotificationChannel),
  }
}

export function normalizeAdminAlertRules(input: ApiAdminAlertRulesResponse): AdminAlertRulesData {
  return {
    rules: (input.rules ?? []).map(normalizeAdminAlertRule),
  }
}

export function serializeAdminSettingsUpdate(input: AdminSettingsUpdateInput) {
  return {
    expected_revision: input.expectedRevision,
    ...(input.siteTitle !== undefined ? { site_title: input.siteTitle } : {}),
    ...(input.logoUrl !== undefined ? { logo_url: input.logoUrl } : {}),
    ...(input.theme !== undefined ? { theme: input.theme } : {}),
    ...(input.agentControllerUrl !== undefined ? { agent_controller_url: input.agentControllerUrl } : {}),
    ...(input.backgroundUrl !== undefined ? { background_url: input.backgroundUrl } : {}),
    ...(input.desktopBackgroundUrl !== undefined ? { desktop_background_url: input.desktopBackgroundUrl } : {}),
    ...(input.mobileBackgroundUrl !== undefined ? { mobile_background_url: input.mobileBackgroundUrl } : {}),
    ...(input.appearancePreset !== undefined ? { appearance_preset: input.appearancePreset } : {}),
    ...(input.serverCardTheme !== undefined ? { server_card_theme: input.serverCardTheme } : {}),
    ...(input.cardOpacity !== undefined ? { card_opacity: input.cardOpacity } : {}),
    ...(input.cardBlur !== undefined ? { card_blur: input.cardBlur } : {}),
    ...(input.cardRadius !== undefined ? { card_radius: input.cardRadius } : {}),
    ...(input.borderStrength !== undefined ? { border_strength: input.borderStrength } : {}),
    ...(input.shadowStrength !== undefined ? { shadow_strength: input.shadowStrength } : {}),
    ...(input.backgroundOverlay !== undefined ? { background_overlay: input.backgroundOverlay } : {}),
    ...(input.themeColor !== undefined ? { theme_color: input.themeColor } : {}),
    ...(input.customCode !== undefined ? { custom_code: input.customCode } : {}),
  }
}

function serializeAdminNodeShared(input: AdminNodeSharedInput, afterLocation: Record<string, unknown> = {}) {
  return {
    ...(input.countryCode !== undefined ? { country_code: input.countryCode } : {}),
    ...(input.region !== undefined ? { region: input.region } : {}),
    ...afterLocation,
    ...(input.expiryDate !== undefined ? { expiry_date: input.expiryDate } : {}),
    ...(input.expiryPermanent !== undefined ? { expiry_permanent: input.expiryPermanent } : {}),
    ...(input.billingCycle !== undefined ? { billing_cycle: input.billingCycle } : {}),
    ...(input.renewalAmount !== undefined ? { renewal_amount: input.renewalAmount } : {}),
    ...(input.renewalCurrency !== undefined ? { renewal_currency: input.renewalCurrency } : {}),
    ...(input.billingMode !== undefined ? { billing_mode: input.billingMode } : {}),
    ...(input.monthlyResetDay !== undefined ? { monthly_reset_day: input.monthlyResetDay } : {}),
    ...(input.displayOrder !== undefined ? { display_order: input.displayOrder } : {}),
    ...(input.publicIPv4 !== undefined ? { public_ipv4: input.publicIPv4 } : {}),
    ...(input.publicIPv6 !== undefined ? { public_ipv6: input.publicIPv6 } : {}),
    ...(input.monthlyQuotaBytes !== undefined ? { monthly_quota_bytes: input.monthlyQuotaBytes } : {}),
    ...(input.disabled !== undefined ? { disabled: input.disabled } : {}),
  }
}

export function serializeAdminNodeUpdate(input: AdminNodeUpdateInput) {
  return {
    ...(input.displayName !== undefined ? { display_name: input.displayName } : {}),
    ...serializeAdminNodeShared(input, input.homeProbeTargetId !== undefined ? { home_probe_target_id: input.homeProbeTargetId } : {}),
    ...(input.probeTargetIds !== undefined ? { probe_target_ids: input.probeTargetIds } : {}),
  }
}

export function serializeAdminNodeCreate(input: AdminNodeCreateInput) {
  return {
    ...(input.id !== undefined && input.id.trim() !== '' ? { id: input.id } : {}),
    display_name: input.displayName,
    ...serializeAdminNodeShared(input),
  }
}

function serializeAdminProbeTargetShared(input: AdminProbeTargetSharedInput) {
  return {
    ...(input.name !== undefined ? { name: input.name } : {}),
    ...(input.type !== undefined ? { type: input.type } : {}),
    ...(input.address !== undefined ? { address: input.address } : {}),
    ...(input.port !== undefined ? { port: input.port } : {}),
    ...(input.count !== undefined ? { count: input.count } : {}),
    ...(input.timeoutMs !== undefined ? { timeout_ms: input.timeoutMs } : {}),
    ...(input.intervalSec !== undefined ? { interval_sec: input.intervalSec } : {}),
    ...(input.displayOrder !== undefined ? { display_order: input.displayOrder } : {}),
    ...(input.assignments !== undefined ? {
      assignments: input.assignments.map((assignment) => ({
        node_id: assignment.nodeId,
        enabled: assignment.enabled,
      })),
    } : {}),
  }
}

export function serializeAdminProbeTargetCreate(input: AdminProbeTargetInput) {
  return {
    ...(input.id !== undefined && input.id.trim() !== '' ? { id: input.id } : {}),
    ...serializeAdminProbeTargetShared(input),
  }
}

export function serializeAdminProbeTargetUpdate(input: AdminProbeTargetUpdateInput) {
  return serializeAdminProbeTargetShared(input)
}

export function serializeAdminNotificationChannelCreate(input: AdminNotificationChannelCreateInput) {
  return {
    ...(input.id !== undefined && input.id.trim() !== '' ? { id: input.id } : {}),
    name: input.name,
    destination: input.destination,
    credential: input.credential,
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
  }
}

export function serializeAdminNotificationChannelUpdate(input: AdminNotificationChannelUpdateInput) {
  const trimmedCredential = input.credential?.trim()
  return {
    ...(input.name !== undefined ? { name: input.name } : {}),
    ...(input.destination !== undefined ? { destination: input.destination } : {}),
    ...(trimmedCredential !== undefined && trimmedCredential !== '' ? { credential: trimmedCredential } : {}),
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
  }
}

export function serializeAdminAlertRuleUpdate(input: AdminAlertRuleUpdateInput) {
  return {
    ...(input.enabled !== undefined ? { enabled: input.enabled } : {}),
    ...(input.threshold !== undefined ? { threshold: input.threshold } : {}),
    ...(input.renewalDays !== undefined ? { renewal_days: input.renewalDays } : {}),
    ...(input.durationSec !== undefined ? { duration_sec: input.durationSec } : {}),
    ...(input.scopeNodeIds !== undefined ? { scope_node_ids: input.scopeNodeIds } : {}),
  }
}

export function normalizeAdminNode(node: ApiAdminNode): AdminNode {
  return {
    id: node.id,
    displayName: node.display_name,
    status: node.status,
    countryCode: node.country_code,
    region: node.region,
    homeProbeTargetId: node.home_probe_target_id,
    disabled: node.disabled,
    billingMode: node.billing_mode,
    monthlyResetDay: node.monthly_reset_day ?? 1,
    expiryDate: node.expiry_date,
    expiryPermanent: Boolean(node.expiry_permanent),
    billingCycle: node.billing_cycle,
    renewalAmount: node.renewal_amount ?? null,
    renewalCurrency: node.renewal_currency ?? 'CNY',
    displayOrder: node.display_order ?? 0,
    publicIPv4: node.public_ipv4,
    publicIPv6: node.public_ipv6,
    monthlyQuotaBytes: node.monthly_quota_bytes ?? null,
    lastSeenAt: node.last_seen_at ?? undefined,
    createdAt: node.created_at,
    updatedAt: node.updated_at,
    hostname: node.hostname,
    osName: node.os_name,
    osVersion: node.os_version,
    kernel: node.kernel,
    arch: node.arch,
    virtualization: node.virtualization,
    cpuModel: node.cpu_model,
    cpuCores: node.cpu_cores ?? null,
    memoryTotalBytes: node.memory_total_bytes ?? null,
    diskTotalBytes: node.disk_total_bytes ?? null,
    bootTime: node.boot_time ?? undefined,
    agentVersion: node.agent_version,
  }
}

export function normalizeAdminProbeTarget(target: ApiAdminProbeTarget): AdminProbeTarget {
  return {
    id: target.id,
    name: target.name,
    type: target.type,
    address: target.address,
    port: target.port ?? null,
    count: target.count,
    timeoutMs: target.timeout_ms,
    intervalSec: target.interval_sec,
    displayOrder: target.display_order ?? 0,
    assignments: (target.assignments ?? []).map((assignment) => ({
      nodeId: assignment.node_id,
      nodeDisplayName: assignment.node_display_name,
      enabled: assignment.enabled,
    })),
  }
}

export function normalizeAdminNotificationChannel(channel: ApiAdminNotificationChannel): AdminNotificationChannel {
  return {
    id: channel.id,
    name: channel.name,
    destination: channel.destination,
    credentialSet: channel.credential_set,
    enabled: channel.enabled,
    createdAt: channel.created_at,
    updatedAt: channel.updated_at,
  }
}

export function normalizeAdminNotificationDelivery(delivery: ApiAdminNotificationDelivery): AdminNotificationDelivery {
  return {
    id: delivery.id,
    eventType: delivery.event_type,
    label: delivery.label,
    nodeId: delivery.node_id,
    nodeName: delivery.node_name,
    previousStatus: delivery.previous_status,
    status: delivery.status,
    channelId: delivery.channel_id,
    channelName: delivery.channel_name,
    success: delivery.success,
    error: delivery.error,
    createdAt: delivery.created_at,
  }
}

export function normalizeAdminAlertRule(rule: ApiAdminAlertRule): AdminAlertRule {
  return {
    id: rule.id,
    name: rule.name,
    category: rule.category,
    metric: rule.metric,
    comparator: rule.comparator,
    threshold: rule.threshold,
    renewalDays: rule.renewal_days ?? [],
    thresholdUnit: rule.threshold_unit,
    durationSec: rule.duration_sec,
    enabled: rule.enabled,
    notificationEventType: rule.notification_event_type,
    notificationLabel: rule.notification_label,
    description: rule.description,
    scopeNodeIds: rule.scope_node_ids ?? [],
    createdAt: rule.created_at,
    updatedAt: rule.updated_at,
  }
}
