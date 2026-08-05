import { type ReactNode, useState } from 'react'
import { availableHistoryRanges } from '../lib/historyRange'
import type { LatencyPoint, ServiceTarget } from '../types'
import { LatencyChart } from './LatencyChart'
import { HistoryRangeSelector } from './HistoryRangeSelector'

export interface ServiceDetailProps {
  target: ServiceTarget
  points: LatencyPoint[]
  range: string
  loading?: boolean
  error?: string
  canUseExtendedRanges?: boolean
  onBack: () => void
  onRangeChange: (range: string) => void
  topHeader?: ReactNode
}

export function ServiceDetail({ target, points, range, loading, error, canUseExtendedRanges = false, onBack, onRangeChange, topHeader }: ServiceDetailProps) {
  const [peakCut, setPeakCut] = useState(false)
  const serviceRangeOptions = availableHistoryRanges(canUseExtendedRanges)
  const rangeLabel = serviceRangeOptions.find((option) => option.value === range)?.label ?? range
  return (
    <div className="kulin-container detail-container">
      <section className="home-top-card detail-top-card" aria-label={`${target.name} service overview`}>
        {topHeader}
        <section className="detail-hero">
          <div className="detail-hero__main">
            <button className="detail-title-button" type="button" onClick={onBack}>
              <span aria-hidden="true">‹</span>
              <span>{target.name}</span>
            </button>
            <span className={`detail-status-pill status-${serviceTone(target)}`}>{target.reportingNodeCount} / {target.assignedNodeCount} 节点上报</span>
          </div>
          <section className="detail-fact-strip detail-fact-strip--service" aria-label={`${target.name} service facts`}>
            <ServiceInfoFact label="类型" value={target.type} />
            <ServiceInfoFact label="最新延迟" value={formatServiceLatency(target.avgMs)} />
            <ServiceInfoFact label="丢包" value={formatServiceLoss(target.lossPercent)} />
            <ServiceInfoFact label="更新时间" value={target.updatedAt ? formatAdminDate(target.updatedAt) : '--'} />
          </section>
        </section>
      </section>

      <section className="monitor-panel" aria-label={`${target.name} service latency`}>
        <header className="monitor-heading">
          <div>
            <h3>{target.name} 多节点历史</h3>
            <p>{rangeLabel} · 按节点分线展示</p>
          </div>
          <div className="monitor-heading-actions">
            <HistoryRangeSelector
              ariaLabel="service latency range selector"
              options={serviceRangeOptions}
              value={range}
              onChange={onRangeChange}
            />
            <label className="peak-switch">
              <input type="checkbox" aria-label="平滑" checked={peakCut} onChange={(event) => setPeakCut(event.target.checked)} />
              <span />
              <b>平滑</b>
            </label>
          </div>
        </header>
        {loading && <div className="detail-state">正在读取服务延迟…</div>}
        {error && <div className="detail-state is-error">服务延迟读取失败：{error}</div>}
        {!loading && !error && points.length === 0 && <div className="detail-state">暂无服务延迟历史</div>}
        {!loading && !error && points.length > 0 && (
          <LatencyChart points={points} title={`${target.name} 多节点延迟`} eyebrow={`${rangeLabel} · ${target.reportingNodeCount} 个节点`} compactHeader peakCut={peakCut} />
        )}
      </section>
    </div>
  )
}

function ServiceInfoFact({ label, value }: { label: string; value: string }) {
  return (
    <article className="detail-fact" title={`${label}: ${value}`}>
      <p>{label}</p>
      <strong>{value}</strong>
    </article>
  )
}

function serviceTone(service: ServiceTarget): 'online' | 'warning' | 'offline' {
  if (service.reportingNodeCount <= 0) return 'offline'
  if (service.assignedNodeCount > 0 && service.reportingNodeCount < service.assignedNodeCount) return 'warning'
  if (service.lossPercent !== null && service.lossPercent >= 20) return 'warning'
  return 'online'
}

function formatServiceLatency(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${Number.isInteger(value) ? value.toFixed(0) : value.toFixed(2)}ms`
}

function formatServiceLoss(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--'
  return `${value.toFixed(2)}%`
}

function formatAdminDate(value?: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}
