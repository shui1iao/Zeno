import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { HomeCardNode, LatencyPoint, StatePoint } from '../types'
import { formatLatency } from '../lib/format'
import { summarizeLatencyTargets } from '../lib/latencyTargets'
import { latencySeriesColor, LatencyChart } from './LatencyChart'
import { ServerFlag } from './ServerFlag'
import { StateHistoryPanel } from './StateHistoryPanel'
import { availableHistoryRanges } from '../lib/historyRange'
import { HistoryRangeSelector } from './HistoryRangeSelector'

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
}

export function LatencyDetail({
  node,
  points,
  statePoints = [],
  range,
  stateRange = '1h',
  loading = false,
  error,
  stateLoading = false,
  stateError,
  canUseExtendedRanges = false,
  onBack,
  onRangeChange,
  onStateRangeChange = () => {},
  topHeader,
}: LatencyDetailProps) {
  const targetSummaries = useMemo(() => summarizeLatencyTargets(points), [points])
  const [activeTargetIds, setActiveTargetIds] = useState<string[]>([])
  const [peakCut, setPeakCut] = useState(false)
  const rangeOptions = availableHistoryRanges(canUseExtendedRanges)
  const rangeLabel = rangeOptions.find((option) => option.value === range)?.label ?? range
  const latestState = latestStatePoint(statePoints)
  const visualStatus = node.status === 'online' ? 'online' : 'offline'
  const summaryUptimeSeconds = node.uptimeSeconds ?? null
  const uptimeValue = latestState?.uptimeSeconds !== null && latestState?.uptimeSeconds !== undefined
    ? formatUptime(latestState.uptimeSeconds)
    : summaryUptimeSeconds !== null
      ? formatUptime(summaryUptimeSeconds)
      : formatUptimeFromBootTime(node.bootTime)
  const loadValue = latestState && latestState.load1 !== null && latestState.load5 !== null && latestState.load15 !== null
    ? `${formatFixed(latestState.load1, 2)} / ${formatFixed(latestState.load5, 2)} / ${formatFixed(latestState.load15, 2)}`
    : node.load1 !== null && node.load1 !== undefined && node.load5 !== null && node.load5 !== undefined && node.load15 !== null && node.load15 !== undefined
      ? `${formatFixed(node.load1, 2)} / ${formatFixed(node.load5, 2)} / ${formatFixed(node.load15, 2)}`
    : '-- / -- / --'
  const memoryUsageValue = formatBinaryUsage(
    latestState?.memoryUsedBytes ?? node.memoryUsedBytes,
    latestState?.memoryTotalBytes ?? node.memoryTotalBytes,
  )
  const diskUsageValue = formatBinaryUsage(
    latestState?.diskUsedBytes ?? node.diskUsedBytes,
    latestState?.diskTotalBytes ?? node.diskTotalBytes,
  )
  const hasLatencyData = points.length > 0 || targetSummaries.length > 0
  const showLatencySkeleton = loading && !hasLatencyData && !error
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
            <InfoFact label="内存" value={memoryUsageValue} />
            <InfoFact label="磁盘" value={diskUsageValue} />
            <InfoFact label="开机时间" value={formatBootTime(node.bootTime)} />
            <InfoFact label="运行时间" value={uptimeValue ?? '--'} adaptive pending={!uptimeValue} />
            <InfoFact label="负载" value={loadValue} adaptive pending={loadValue === '-- / -- / --'} />
            <TrafficFact sent={formatBinaryBytes(node.netOutTotalBytes)} received={formatBinaryBytes(node.netInTotalBytes)} />
          </section>
        </section>
      </section>

      <section className="monitor-panel" aria-label={`${node.displayName} network latency`}>
        <header className="monitor-heading latency-monitor-heading">
          <div className="monitor-heading-title">
            <div className="monitor-title-row">
              <h3>网络延迟</h3>
              <label className="peak-switch">
                <input type="checkbox" aria-label="平滑" checked={peakCut} onChange={(event) => setPeakCut(event.target.checked)} />
                <span />
                <b>平滑</b>
              </label>
            </div>
            <p>{showLatencySkeleton ? '同步监控服务…' : `${targetSummaries.length} 个监控服务`}</p>
          </div>
          <div className="monitor-heading-actions">
            <HistoryRangeSelector
              ariaLabel="latency range selector"
              options={rangeOptions}
              value={range}
              onChange={onRangeChange}
            />
          </div>
        </header>

        {showLatencySkeleton && <LatencyLoadingSkeleton />}
        {error && <div className="detail-state is-error">网络延迟读取失败：{error}</div>}

        {!showLatencySkeleton && !error && hasLatencyData && (
          <>
            <div className="latency-target-grid" aria-label="monitor services">
              {targetSummaries.map((target, index) => (
                <button
                  key={target.targetId}
                  type="button"
                  title={`${target.targetName} · ${formatLatency(target.avgMs)} · 丢包 ${formatLossPercent(target.lossPercent)}`}
                  data-active={activeTargetIds.includes(target.targetId)}
                  onClick={() => toggleTarget(target.targetId)}
                >
                  <span className="latency-target-name">
                    <i className="latency-target-color" style={{ backgroundColor: latencySeriesColor(index) }} aria-hidden="true" />
                    <span>{target.targetName}</span>
                  </span>
                  <strong>{formatLatency(target.avgMs)}</strong>
                  <em>丢包 {formatLossPercent(target.lossPercent)}</em>
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
            />
          </>
        )}
        {!showLatencySkeleton && !error && !hasLatencyData && <div className="detail-state">暂无网络延迟历史</div>}
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
      <div className="latency-target-grid is-loading" aria-label="monitor services loading" aria-hidden="true">
        {Array.from({ length: 7 }).map((_, index) => (
          <button key={index} type="button" disabled>
            <span>同步中</span>
            <strong>-- ms</strong>
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

function InfoFact({ label, value, wide = false, adaptive = false, pending = false }: { label: string; value: string; wide?: boolean; adaptive?: boolean; pending?: boolean }) {
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
      <strong>{adaptive ? <span ref={valueRef}>{value}</span> : value}</strong>
    </article>
  )
}

function TrafficFact({ sent, received }: { sent: string; received: string }) {
  return (
    <article className="detail-fact detail-fact--traffic" title={`累计流量: ↑ ${sent}  ↓ ${received}`}>
      <p>累计流量</p>
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
  return status === 'online' ? '在线' : '离线'
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

function formatBootTime(value: string | undefined): string {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
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

function formatBinaryUsage(used: number | null | undefined, total: number | null | undefined): string {
  if ((used === null || used === undefined) && (total === null || total === undefined)) return '--'
  return `${formatBinaryBytes(used)} / ${formatBinaryBytes(total)}`
}

function formatLossPercent(value: number | null | undefined): string {
  if (value === null || value === undefined) return 'No data'
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

function formatFixed(value: number, digits: number): string {
  return value.toFixed(digits)
}

function BackIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m15 18-6-6 6-6" />
    </svg>
  )
}
