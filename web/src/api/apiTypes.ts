import type { AdminAlertRule, AdminNode, AdminNotificationChannel, AdminProbeTarget, AdminTheme, AppearancePreset, HomeCardNode, LatencyPoint, ProbeType, ServerCardTheme, ServiceTarget, StatePoint } from '../types'
import type { CurrencyRates } from '../lib/currency'

export interface ApiSettings {
  site_title: string
  logo_url: string
  theme: AdminTheme
  agent_controller_url?: string
  background_url: string
  desktop_background_url?: string
  mobile_background_url?: string
  appearance_preset?: AppearancePreset
  server_card_theme?: ServerCardTheme
  card_opacity?: number
  card_blur?: number
  card_radius?: number
  border_strength?: number
  shadow_strength?: number
  background_overlay?: number
  theme_color?: string
  custom_code?: string
  revision?: number
  updated_at?: string
}

export interface ApiHourlyLatencyPoint {
  started_at: string
  latency_ms: number | null
  loss_percent: number | null
}

export interface ApiLatencySummary {
  target_id: string
  target_name: string
  median_ms: number | null
  avg_ms: number | null
  loss_percent: number | null
  updated_at: string
  hourly_history?: ApiHourlyLatencyPoint[] | null
}

export interface ApiNode {
  id: string
  display_name: string
  status: HomeCardNode['status']
  os: string
  os_version?: string
  kernel?: string
  arch?: string
  virtualization?: string
  cpu_model?: string
  country_code?: string
  subtitle?: string
  cpu_cores?: number | null
  expiry_label?: string
  renewal_amount?: number | null
  renewal_currency?: string
  billing_cycle?: string
  monthly_cost_cny?: number | null
  cpu_percent: number | null
  memory_used_bytes: number | null
  memory_total_bytes: number | null
  disk_used_bytes: number | null
  disk_total_bytes: number | null
  boot_time?: string | null
  load1?: number | null
  load5?: number | null
  load15?: number | null
  uptime_seconds?: number | null
  net_in_speed_bps: number | null
  net_out_speed_bps: number | null
  net_in_total_bytes: number | null
  net_out_total_bytes: number | null
  net_in_lifetime_bytes?: number | null
  net_out_lifetime_bytes?: number | null
  billing_mode?: string
  monthly_reset_day?: number
  monthly_period_start?: string
  monthly_period_end?: string
  monthly_billable_bytes: number | null
  monthly_quota_bytes: number | null
  latency_summary?: ApiLatencySummary
  latency_summaries?: ApiLatencySummary[] | null
}

export interface ApiLatencyPoint {
  ts: string
  target_id: string
  target_name: string
  median_ms: number | null
  avg_ms?: number | null
  loss_percent: number
}

export interface ApiLatencySeries {
  target_id: string
  target_name: string
  created_at?: number[] | null
  median_ms?: Array<number | null> | null
  avg_ms?: Array<number | null> | null
  loss_percent?: number[] | null
}

export interface ApiServiceTarget {
  id: string
  name: string
  type: ProbeType
  assigned_node_count: number
  reporting_node_count: number
  median_ms: number | null
  avg_ms?: number | null
  loss_percent: number | null
  updated_at?: string
}

export interface ApiServiceLatencyPoint {
  ts: string
  node_id: string
  node_name: string
  median_ms: number | null
  avg_ms?: number | null
  loss_percent: number
}

export interface ApiServiceLatencySeries {
  node_id: string
  node_name: string
  created_at?: number[] | null
  median_ms?: Array<number | null> | null
  avg_ms?: Array<number | null> | null
  loss_percent?: number[] | null
}

export interface ApiStatePoint {
  ts: string
  cpu_percent: number | null
  load1?: number | null
  load5?: number | null
  load15?: number | null
  memory_used_bytes: number | null
  memory_total_bytes: number | null
  swap_used_bytes?: number | null
  swap_total_bytes?: number | null
  disk_used_bytes: number | null
  disk_total_bytes: number | null
  net_in_total_bytes: number | null
  net_out_total_bytes: number | null
  net_in_speed_bps: number | null
  net_out_speed_bps: number | null
  process_count?: number | null
  tcp_connection_count?: number | null
  udp_connection_count?: number | null
  uptime_seconds: number | null
}

export interface ApiStateSeries {
  cpu_percent?: Array<number | null> | null
  load1?: Array<number | null> | null
  load5?: Array<number | null> | null
  load15?: Array<number | null> | null
  memory_used_bytes?: Array<number | null> | null
  memory_total_bytes?: Array<number | null> | null
  swap_used_bytes?: Array<number | null> | null
  swap_total_bytes?: Array<number | null> | null
  disk_used_bytes?: Array<number | null> | null
  disk_total_bytes?: Array<number | null> | null
  net_in_total_bytes?: Array<number | null> | null
  net_out_total_bytes?: Array<number | null> | null
  net_in_speed_bps?: Array<number | null> | null
  net_out_speed_bps?: Array<number | null> | null
  process_count?: Array<number | null> | null
  tcp_connection_count?: Array<number | null> | null
  udp_connection_count?: Array<number | null> | null
  uptime_seconds?: Array<number | null> | null
}

export interface ApiAdminNode {
  id: string
  display_name: string
  status: string
  country_code?: string
  region?: string
  home_probe_target_id?: string
  disabled: boolean
  billing_mode: string
  monthly_reset_day: number
  expiry_date?: string
  expiry_permanent?: boolean
  billing_cycle?: string
  renewal_amount?: number | null
  renewal_currency?: string
  display_order?: number
  public_ipv4?: string
  public_ipv6?: string
  monthly_quota_bytes?: number | null
  last_seen_at?: string | null
  created_at: string
  updated_at: string
  hostname?: string
  os_name?: string
  os_version?: string
  kernel?: string
  arch?: string
  virtualization?: string
  cpu_model?: string
  cpu_cores?: number | null
  memory_total_bytes?: number | null
  disk_total_bytes?: number | null
  boot_time?: string | null
  agent_version?: string
}

export interface ApiAdminProbeTargetAssignment {
  node_id: string
  node_display_name: string
  enabled: boolean
}

export interface ApiAdminProbeTarget {
  id: string
  name: string
  type: ProbeType
  address: string
  port: number | null
  count: number
  timeout_ms: number
  interval_sec: number
  display_order?: number
  assignments: ApiAdminProbeTargetAssignment[] | null
}

export interface ApiAdminNotificationChannel {
  id: string
  name: string
  destination: string
  credential_set: boolean
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface ApiAdminNotificationDelivery {
  id: number
  event_type: string
  label: string
  node_id: string
  node_name: string
  previous_status: string
  status: string
  channel_id: string
  channel_name: string
  success: boolean
  error?: string
  created_at: string
}

export interface ApiAdminAlertRule {
  id: string
  name: string
  category: string
  metric: string
  comparator: string
  threshold: number
  renewal_days?: number[] | null
  threshold_unit: string
  duration_sec: number
  enabled: boolean
  notification_event_type: string
  notification_label: string
  description: string
  scope_node_ids?: string[] | null
  created_at: string
  updated_at: string
}

export interface ApiAdminSettingsResponse {
  settings: ApiSettings
}

export interface ApiSummaryResponse {
  nodes: ApiNode[] | null
  services?: ApiServiceTarget[] | null
  latency_points: ApiLatencyPoint[] | null
  exchange_rates?: Record<string, number> | null
}

export interface ApiLatencyResponse {
  node_id: string
  range: string
  created_at?: number[] | null
  points?: ApiLatencyPoint[] | null
  series?: ApiLatencySeries[] | null
}

export interface ApiServiceLatencyResponse {
  target: ApiServiceTarget
  range: string
  created_at?: number[] | null
  points?: ApiServiceLatencyPoint[] | null
  series?: ApiServiceLatencySeries[] | null
}

export interface ApiStateResponse {
  node_id: string
  range: string
  points?: ApiStatePoint[] | null
  created_at?: number[] | null
  series?: ApiStateSeries | null
}

export interface ApiAdminNodesResponse {
  nodes: ApiAdminNode[] | null
}

export interface ApiAdminNodeResponse {
  node: ApiAdminNode
}

export interface ApiAdminNodeInstallCommandResponse {
  node_id: string
  command: string
  commands?: Record<string, string>
}

export interface ApiAdminProbeTargetsResponse {
  targets: ApiAdminProbeTarget[]
}

export interface ApiAdminProbeTargetResponse {
  target: ApiAdminProbeTarget
}

export interface ApiAdminNotificationChannelsResponse {
  channels: ApiAdminNotificationChannel[]
}

export interface ApiAdminNotificationChannelResponse {
  channel: ApiAdminNotificationChannel
}

export interface ApiAdminNotificationTestResponse {
  delivery: ApiAdminNotificationDelivery
}

export interface ApiAdminAlertRulesResponse {
  rules: ApiAdminAlertRule[]
}

export interface ApiAdminAlertRuleResponse {
  rule: ApiAdminAlertRule
}

export interface SummaryData {
  nodes: HomeCardNode[]
  services: ServiceTarget[]
  latencyPoints: LatencyPoint[]
  exchangeRates: CurrencyRates
}

export interface NodeLatencyData {
  nodeId: string
  range: string
  points: LatencyPoint[]
  snapshotKey?: string
}

export interface ServiceLatencyData {
  target: ServiceTarget
  range: string
  points: LatencyPoint[]
}

export interface NodeStateData {
  nodeId: string
  range: string
  points: StatePoint[]
}

export interface AdminNodesData {
  nodes: AdminNode[]
}

export interface AdminProbeTargetsData {
  targets: AdminProbeTarget[]
}

export interface AdminNotificationChannelsData {
  channels: AdminNotificationChannel[]
}

export interface AdminAlertRulesData {
  rules: AdminAlertRule[]
}

export interface AdminSettingsUpdateInput {
  expectedRevision: number
  siteTitle?: string
  logoUrl?: string
  theme?: AdminTheme
  agentControllerUrl?: string
  backgroundUrl?: string
  desktopBackgroundUrl?: string
  mobileBackgroundUrl?: string
  appearancePreset?: AppearancePreset
  serverCardTheme?: ServerCardTheme
  cardOpacity?: number
  cardBlur?: number
  cardRadius?: number
  borderStrength?: number
  shadowStrength?: number
  backgroundOverlay?: number
  themeColor?: string
  customCode?: string
}

export interface AdminNodeSharedInput {
  countryCode?: string
  region?: string
  expiryDate?: string
  expiryPermanent?: boolean
  billingCycle?: string
  renewalAmount?: number | null
  renewalCurrency?: string
  billingMode?: string
  monthlyResetDay?: number
  displayOrder?: number
  publicIPv4?: string
  publicIPv6?: string
  monthlyQuotaBytes?: number | null
  disabled?: boolean
}

export interface AdminNodeUpdateInput extends AdminNodeSharedInput {
  displayName?: string
  homeProbeTargetId?: string
  probeTargetIds?: string[]
}

export interface AdminNodeCreateInput extends AdminNodeSharedInput {
  id?: string
  displayName: string
}

export interface AdminProbeTargetSharedInput {
  name?: string
  type?: ProbeType
  address?: string
  port?: number | null
  count?: number
  timeoutMs?: number
  intervalSec?: number
  displayOrder?: number
  assignments?: Array<{ nodeId: string; enabled: boolean }>
}

export interface AdminProbeTargetInput extends AdminProbeTargetSharedInput {
  id?: string
  name: string
  type: ProbeType
  address: string
  port: number | null
  count: number
  timeoutMs: number
  intervalSec: number
}

export interface AdminProbeTargetUpdateInput extends AdminProbeTargetSharedInput {}

export interface AdminNotificationChannelCreateInput {
  id?: string
  name: string
  destination: string
  credential: string
  enabled?: boolean
}

export interface AdminNotificationChannelUpdateInput {
  name?: string
  destination?: string
  credential?: string
  enabled?: boolean
}

export interface AdminAlertRuleUpdateInput {
  enabled?: boolean
  threshold?: number
  renewalDays?: number[]
  durationSec?: number
  scopeNodeIds?: string[]
}
