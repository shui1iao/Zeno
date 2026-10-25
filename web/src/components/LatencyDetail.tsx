import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { HomeCardNode, LatencyPoint, StatePoint } from '../types'
import { formatLatency, formatTooltipDateTime } from '../lib/format'
import { summarizeLatencyTargets } from '../lib/latencyTargets'
import { seriesColorMap } from '../lib/seriesColors'
import { LatencyChart } from './LatencyChart'
import { BackIcon, PacketLossIcon, packetLossTone } from './icons'
import { ServerFlag } from './ServerFlag'
import { StateHistoryPanel } from './StateHistoryPanel'
import { availableHistoryRanges } from '../lib/historyRange'
import { HistoryRangeSelector } from './HistoryRangeSelector'
import { SmoothingSelector, type SmoothingMode } from './SmoothingSelector'
import { latencyTargetCorners } from '../lib/targetGridCorners'

const DESKTOP_LATENCY_COLUMNS = 7
const MOBILE_LATENCY_COLUMNS = 3

interface LatencyDetailProps {
  node: HomeCardNode
  points: LatencyPoint[]
  statePoints?: StatePoint[]
  range: string
  stateRange?: string
  loading?: boolean
  error?: string
  stateLoading?: boolean
  stateError?: string
  canUseExtendedRanges?: boolean
  onBack: () => void
  onRangeChange: (range: string) => void
  onStateRangeChange?: (range: string) => void
  topHeader?: ReactNode
  /** Global latency-target order from the admin list; drives series colours. */
  targetOrder?: string[]
}

export function LatencyDetail({
  node,
  points,
  statePoints = [],
  range,
  stateRange = '1d',
  loading = false,
  error,
  stateLoading = false,
  stateError,
  canUseExtendedRanges = false,
  onBack,
  onRangeChange,
  onStateRangeChange = () => {},
  topHeader,
  targetOrder = [],
}: LatencyDetailProps) {
  const targetSummaries = useMemo(() => summarizeLatencyTargets(points), [points])
  const targetOrderKey = targetOrder.join('\n')
  const seriesColors = useMemo(
    () => seriesColorMap(targetOrder, targetSummaries.map((target) => target.targetId)),
    [targetOrderKey, targetSummaries],
  )
  const [activeTargetIds, setActiveTargetIds] = useState<string[]>([])
  const [smoothing, setSmoothing] = useState<SmoothingMode>('raw')
  const peakCut = smoothing === 'smooth'
  const rangeOptions = availableHistoryRanges(canUseExtendedRanges)
  const rangeLabel = rangeOptions.find((option) => option.value === range)?.label ?? range
  const latestState = latestStatePoint(statePoints)
  const visualStatus = node.status === 'warning' ? 'warning' : node.status === 'online' ? 'online' : 'offline'
  const summaryUptimeSeconds = node.uptimeSeconds ?? null
  const uptimeValue = latestState?.uptimeSeconds !== null && latestState?.uptimeSeconds !== undefined
    ? formatUptime(latestState.uptimeSeconds)
    : summaryUptimeSeconds !== null
      ? formatUptime(summaryUptimeSeconds)
      : formatUptimeFromBootTime(node.bootTime)
  // Memory, disk and load are left to the resource panel below and the home card;
  // the header keeps what neither of them shows.
  const bootValue = formatBootShort(node.bootTime)
  const runtimeValue = uptimeValue ? (bootValue ? `${uptimeValue} · ${bootValue} 起` : uptimeValue) : null
  const hasLatencyData = points.length > 0 || targetSummaries.length > 0
  const showLatencySkeleton = loading && !hasLatencyData && !error
  const desktopLatencyLastRowStart = Math.floor((targetSummaries.length - 1) / DESKTOP_LATENCY_COLUMNS) * DESKTOP_LATENCY_COLUMNS
  const mobileLatencyLastRowStart = Math.floor((targetSummaries.length - 1) / MOBILE_LATENCY_COLUMNS) * MOBILE_LATENCY_COLUMNS
  const toggleTarget = (targetId: string) => {
    setActiveTargetIds((current) => (
      current.includes(targetId) ? current.filter((id) => id !== targetId) : [...current, targetId]
    ))
  }

  return (
    <div className="kulin-container detail-container">
      <section className="home-top-card detail-top-card" aria-label={`${node.displayName} server overview`}>
        {topHeader}
        <section className="detail-hero">
          <div className="detail-hero__main">
            <button className="detail-title-button" type="button" onClick={onBack}>
              <BackIcon />
              <ServerFlag countryCode={node.countryCode} className="detail-title-flag" />
              <span>{node.displayName}</span>
            </button>
            <div className="detail-hero__badges" aria-label="server live status">
              <span className={`detail-status-pill status-${visualStatus}`}>{formatStatusLabel(node.status)}</span>
            </div>
          </div>
          <section className="detail-fact-strip detail-fact-strip--server" aria-label={`${node.displayName} server facts`}>
            <InfoFact label="系统" value={formatSystemSpec(node)} wide />
            <InfoFact label="CPU" value={formatCpuSpec(node)} wide />
            <InfoFact label="平台" value={formatPlatform(node.virtualization)} />
            <InfoFact label="运行时间" value={runtimeValue ?? '--'} adaptive pending={!runtimeValue} />
            <TrafficFact sent={formatBinaryBytes(node.netOutTotalBytes)} received={formatBinaryBytes(node.netInTotalBytes)} />
          </section>
        </section>
      </section>

      <section className="monitor-panel" aria-label={`${node.displayName} network latency`}>
        <header className="monitor-heading latency-monitor-heading">
          <div className="monitor-heading-title">
            <div className="monitor-title-row">
              <h3>网络延迟</h3>
              <SmoothingSelector value={smoothing} onChange={setSmoothing} />
            </div>
            <p>{showLatencySkeleton ? '同步监控服务…' : `${targetSummaries.length} 个监控服务`}</p>
          </div>
          <div className="monitor-heading-actions">
            <HistoryRangeSelector
              ariaLabel="latency range selector"
              options={rangeOptions}
              value={range}
              onChange={onRangeChange}
              commitDelayMs={190}
            />
          </div>
        </header>

        {showLatencySkeleton && <LatencyLoadingSkeleton />}
        {error && <div className="detail-state is-error" data-state="error" role="alert">网络延迟读取失败：{error}</div>}

        {!showLatencySkeleton && !error && hasLatencyData && (
          <>
            <div
              className="latency-target-grid"
              aria-label="monitor services"
            >
              {targetSummaries.map((target, index) => (
                <button
                    key={target.targetId}
                    type="button"
                    title={`${target.targetName} · ${formatLatency(target.avgMs)} · 丢包 ${formatLossPercent(target.lossPercent)}`}
                    aria-pressed={activeTargetIds.includes(target.targetId)}
                    data-active={activeTargetIds.includes(target.targetId)}
                    data-desktop-corners={latencyTargetCorners(index, targetSummaries.length, DESKTOP_LATENCY_COLUMNS)}
                    data-mobile-corners={latencyTargetCorners(index, targetSummaries.length, MOBILE_LATENCY_COLUMNS)}
                    data-desktop-full-row-end={(index + 1) % DESKTOP_LATENCY_COLUMNS === 0}
                    data-desktop-last-row={index >= desktopLatencyLastRowStart}
                    data-mobile-full-row-end={(index + 1) % MOBILE_LATENCY_COLUMNS === 0}
                    data-mobile-last-row={index >= mobileLatencyLastRowStart}
                    onClick={() => toggleTarget(target.targetId)}
                  >
                    <span className="latency-target-name">
                      <i className="latency-target-color" style={{ backgroundColor: seriesColors.get(target.targetId) }} aria-hidden="true" />
                      <span>{target.targetName}</span>
                    </span>
                    <strong>{formatLatency(target.avgMs)}</strong>
                    <em className="latency-target-loss" data-tone={packetLossTone(target.lossPercent)}><PacketLossIcon />丢包 {formatLossPercent(target.lossPercent)}</em>
                </button>
              ))}
            </div>

            <LatencyChart
              points={points}
              title={`${node.displayName} 网络延迟`}
              eyebrow={`${rangeLabel} · ${targetSummaries.length} 个监控服务${peakCut ? ' · 平滑' : ''}`}
              compactHeader
              hideHeader
              hideLegend
              peakCut={peakCut}
              activeTargetIds={activeTargetIds}
              seriesColors={seriesColors}
            />
          </>
        )}
        {!showLatencySkeleton && !error && !hasLatencyData && <div className="detail-state" data-state="empty" role="status" aria-live="polite">暂无网络延迟历史</div>}
      </section>

      <StateHistoryPanel
        points={statePoints}
        range={stateRange}
        onRangeChange={onStateRangeChange}
        loading={stateLoading}
        error={stateError}
        canUseExtendedRanges={canUseExtendedRanges}
      />
    </div>
  )
}

function LatencyLoadingSkeleton() {
  return (
    <>
      <div className="sr-only" role="status" data-state="loading" aria-live="polite">正在读取网络延迟…</div>
      <div className="latency-target-grid is-loading" aria-label="monitor services loading" aria-hidden="true">
        {Array.from({ length: 7 }).map((_, index) => (
          <button key={index} type="button" disabled>
            <span>同步中</span>
            <strong>--</strong>
            <em>丢包 --</em>
          </button>
        ))}
      </div>
      <section className="latency-panel latency-panel-skeleton" aria-hidden="true">
        <div className="latency-chart-skeleton" />
      </section>
    </>
  )
}

// Values are split into chunks that may wrap between, but never inside: "2 Virtual Cores",
// "1.95 GB" and each load triple stay whole, so a number never parts from its unit.
function factChunks(value: string): string[] {
  return value.split(/( · | \/ )/).reduce<string[]>((chunks, part, index) => {
    if (index % 2 === 1) chunks[chunks.length - 1] += part.trimEnd()
    else chunks.push(part)
    return chunks
  }, [])
}

function FactValue({ value, keepWhole = false }: { value: string; keepWhole?: boolean }) {
  if (keepWhole) return <span className="detail-fact-chunk">{value}</span>
  const chunks = factChunks(value)
  // Long free-form chunks (CPU model names) may still wrap internally.
  // The separating space stays outside the no-wrap spans so that it remains a break opportunity.
  return <>{chunks.map((chunk, index) => <Fragment key={index}>{index > 0 ? ' ' : ''}<span className={chunk.length <= 28 ? 'detail-fact-chunk' : 'detail-fact-chunk is-long'}>{chunk}</span></Fragment>)}</>
}

function InfoFact({ label, value, wide = false, adaptive = false, pending = false, keepWhole = false }: { label: string; value: string; wide?: boolean; adaptive?: boolean; pending?: boolean; keepWhole?: boolean }) {
  const valueRef = useRef<HTMLElement>(null)
  const [multiline, setMultiline] = useState(false)

  useEffect(() => {
    if (!adaptive) return undefined
    const element = valueRef.current
    if (!element) return undefined
    let active = true

    const measure = () => {
      if (!active) return
      const lineHeight = Number.parseFloat(window.getComputedStyle(element).lineHeight)
      const height = element.getBoundingClientRect().height
      setMultiline(Number.isFinite(lineHeight) && height > lineHeight * 1.5)
    }
    measure()

    const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure)
    observer?.observe(element)
    if (element.parentElement) observer?.observe(element.parentElement)
    window.addEventListener('resize', measure)
    void document.fonts?.ready.then(measure)

    return () => {
      active = false
      observer?.disconnect()
      window.removeEventListener('resize', measure)
    }
  }, [adaptive, value])

  return (
    <article className={`detail-fact${wide ? ' is-wide' : ''}${adaptive ? ' is-adaptive' : ''}${multiline ? ' is-multiline' : ''}${pending ? ' is-pending' : ''}`} title={`${label}: ${value}`}>
      <p>{label}</p>
      <strong>{adaptive ? <span ref={valueRef}><FactValue value={value} keepWhole={keepWhole} /></span> : <FactValue value={value} keepWhole={keepWhole} />}</strong>
    </article>
  )
}

function TrafficFact({ sent, received }: { sent: string; received: string }) {
  return (
    <article className="detail-fact detail-fact--traffic" title={`总流量: ↑ ${sent}  ↓ ${received}`}>
      <p>总流量</p>
      <strong className="detail-traffic-values">
        <span className="detail-traffic-value"><span aria-hidden="true">↑</span><span>{sent}</span></span>
        <span className="detail-traffic-value"><span aria-hidden="true">↓</span><span>{received}</span></span>
      </strong>
    </article>
  )
}

function formatUptimeFromBootTime(value: string | undefined): string | null {
  if (!value) return null
  const startedAt = Date.parse(value)
  if (!Number.isFinite(startedAt)) return null
  const seconds = Math.max(0, Math.floor((Date.now() - startedAt) / 1000))
  return formatUptime(seconds)
}

function formatStatusLabel(status: HomeCardNode['status']): string {
  if (status === 'online') return '在线'
  if (status === 'warning') return '异常'
  return '离线'
}

function formatOSLabel(node: HomeCardNode): string {
  return [node.os, node.osVersion].filter(Boolean).join(' ') || '--'
}

function formatSystemSpec(node: HomeCardNode): string {
  return [formatOSLabel(node), node.arch || '--', node.kernel || '--'].filter(Boolean).join(' · ')
}

function formatCpuSpec(node: HomeCardNode): string {
  return [node.cpuModel || '--', formatCores(node.cpuCores, node.virtualization)].filter(Boolean).join(' · ')
}

function formatCores(value: number | null | undefined, virtualization?: string): string {
  const label = coreTypeLabel(virtualization)
  if (value === null || value === undefined) return `-- ${label.plural}`
  const formatted = Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1)
  return `${formatted} ${value === 1 ? label.singular : label.plural}`
}

function coreTypeLabel(virtualization?: string): { singular: string; plural: string } {
  const value = virtualization?.trim().toLowerCase() ?? ''
  if (value === '') return { singular: 'Core', plural: 'Cores' }
  const virtualMarkers = [
    'virtual', 'kvm', 'qemu', 'standard pc', 'i440fx', 'piix', 'vmware', 'xen', 'hyper-v', 'bochs',
    'parallels', 'bhyve', 'openvz', 'lxc', 'docker', 'container', 'cloud', 'ec2', 'compute engine',
    'digitalocean', 'vultr', 'linode', 'example-transit', 'tencent', 'huawei', 'azure', 'google', 'amazon',
  ]
  if (virtualMarkers.some((marker) => value.includes(marker))) {
    return { singular: 'Virtual Core', plural: 'Virtual Cores' }
  }
  return { singular: 'Physical Core', plural: 'Physical Cores' }
}

/** "09/27 21:40" for the runtime fact; null when the boot time is unknown. */
function formatBootShort(value: string | undefined): string | null {
  if (!value) return null
  const timestamp = Date.parse(value)
  return Number.isFinite(timestamp) ? formatTooltipDateTime(timestamp) : null
}

const platformNames: Record<string, string> = {
  kvm: 'KVM', qemu: 'QEMU', xen: 'Xen', vmware: 'VMware', lxc: 'LXC', openvz: 'OpenVZ', docker: 'Docker',
  podman: 'Podman', microsoft: 'Hyper-V', 'hyper-v': 'Hyper-V', oracle: 'VirtualBox', bhyve: 'bhyve', parallels: 'Parallels',
}

/** Hypervisor or hardware product reported by the agent; short probe tokens get their usual spelling. */
export function formatPlatform(value: string | undefined): string {
  const trimmed = value?.trim() ?? ''
  if (trimmed === '') return '--'
  return platformNames[trimmed.toLowerCase()] ?? trimmed
}

function formatBinaryBytes(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  if (value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit += 1
  }
  return `${size.toFixed(unit === 0 ? 0 : 2)} ${units[unit]}`
}

function formatLossPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '--'
  return `${value.toFixed(2)}%`
}

function latestStatePoint(points: StatePoint[]): StatePoint | null {
  return points.length > 0 ? points[points.length - 1] : null
}

function formatUptime(seconds: number): string {
  const safeSeconds = Math.max(0, Math.floor(seconds))
  const days = Math.floor(safeSeconds / 86_400)
  const hours = Math.floor((safeSeconds % 86_400) / 3_600)
  const minutes = Math.floor((safeSeconds % 3_600) / 60)

  if (days > 0) return `${days} 天 ${hours} 小时`
  if (hours > 0) return `${hours} 小时 ${minutes} 分钟`
  return `${Math.max(1, minutes)} 分钟`
}

