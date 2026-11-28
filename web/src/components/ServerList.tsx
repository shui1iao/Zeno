import { memo } from 'react'
import type { HomeCardNode } from '../types'
import type { CurrencyCode, CurrencyRates } from '../lib/currency'
// Everything comes through ServerCard so this lazy chunk only depends on the
// entry chunk and does not split shared helpers into extra preloaded chunks.
import {
  formatLatency,
  ServerFlag,
  formatOnlineDays,
  formatRate,
  formatUsagePercent,
  historyStripSummary,
  historyTitle,
  historyTone,
  nodeCardReadings,
  nodeVisualStatus,
  osAssetFor,
  osKindFor,
  sameServerCardProps,
  UsageTrack,
  useNodeOpenHandlers,
  type ServerCardProps,
} from './ServerCard'

/**
 * Dense one-row-per-node list view. Lives in its own chunk so the default card
 * view does not pay for it in the initial JavaScript budget.
 */
export default function ServerListView({ nodes, displayCurrency, exchangeRates, onOpen, onIntent }: {
  nodes: HomeCardNode[]
  displayCurrency?: CurrencyCode
  exchangeRates?: CurrencyRates
  onOpen?: (nodeId: string) => void
  onIntent?: (nodeId: string) => void
}) {
  return (
    <section className="monitor-panel server-list" aria-label="server list">
      <ServerListHeader />
      {nodes.map((node) => <ServerListRow key={node.id} node={node} displayCurrency={displayCurrency} exchangeRates={exchangeRates} onOpen={onOpen} onIntent={onIntent} />)}
    </section>
  )
}

const ServerListRow = memo(function ServerListRow({ node, displayCurrency = 'CNY', exchangeRates: inputExchangeRates = { CNY: 1 }, onOpen, onIntent }: ServerCardProps) {
  const { memoryPercent, diskPercent, trafficPercent, trafficText, latency, lossText, hourlyHistory, expiryText, billingText, offline } = nodeCardReadings(node, displayCurrency, inputExchangeRates)
  const latencyValue = latency?.avgMs ?? null
  const visualStatus = nodeVisualStatus(node.status)
  const openHandlers = useNodeOpenHandlers(node.id, onOpen, onIntent)
  const latencyTone = historyTone('latency', latencyValue)
  const lossTone = historyTone('loss', latency?.lossPercent ?? null)
  return (
    <div className={`server-list-row${offline ? ' is-offline' : ''}`} aria-label={`${node.displayName} 详情`} {...openHandlers}>
      <div className="server-list-cell server-list-node">
        <div className="server-list-name">
          <i className={`server-list-dot${visualStatus === 'online' ? '' : ` is-${visualStatus}`}`} aria-hidden="true" />
          <ServerFlag countryCode={node.countryCode} className="node-flag" />
          <b>{node.displayName}</b>
          <img alt={node.os || 'linux'} className="node-os" data-os={osKindFor(node.os)} src={osAssetFor(node.os)} />
          <span className="server-list-latency-inline">{offline ? '' : formatLatency(latencyValue).replace('--', '')}</span>
        </div>
        <div className={`server-list-sub${offline ? ' is-offline' : ''}`}>{offline ? '离线' : formatOnlineDays(node.uptimeSeconds)}</div>
      </div>
      <ListMeter className="server-list-cpu" label="CPU" percent={node.cpuPercent} text={formatUsagePercent(node.cpuPercent)} />
      <ListMeter className="server-list-memory" label="内存" percent={memoryPercent} text={formatUsagePercent(memoryPercent)} />
      <ListMeter className="server-list-disk" label="存储" percent={diskPercent} text={formatUsagePercent(diskPercent)} />
      <ListMeter className="server-list-traffic" label="流量" percent={trafficPercent} text={trafficText} compactText={trafficPercent === null ? '--' : `${trafficPercent.toFixed(1)}%`} />
      <div className="server-list-cell server-list-net">
        <span className="metric-up"><span className="metric-icon"><ArrowUpIcon /></span>{formatRate(node.netOutSpeedBps)}</span>
        <span className="metric-down"><span className="metric-icon"><ArrowDownIcon /></span>{formatRate(node.netInSpeedBps)}</span>
      </div>
      <div className="server-list-cell server-list-health">
        <div className="server-list-health__text">
          <strong className={`is-${latencyTone}`}>{formatLatency(latencyValue)}</strong>
          <span> · 丢包 </span>
          <strong className={`is-${lossTone}`}>{lossText}</strong>
        </div>
        <div className="health-history-strip server-list-strip" role="img" aria-label={historyStripSummary('latency', '延迟', hourlyHistory)}>
          {hourlyHistory.map((point, index) => {
            const tones = [historyTone('latency', point.latencyMs), historyTone('loss', point.lossPercent)]
            const tone = tones.includes('danger') ? 'danger' : tones.includes('warning') ? 'warning' : tones[0]
            return <span key={`${point.startedAt || 'empty'}-${index}`} className={`history-cell is-${tone}`} title={`${historyTitle('latency', point)} · ${historyTitle('loss', point).split(' · ')[1]}`} />
          })}
        </div>
      </div>
      <div className="server-list-cell server-list-expiry">
        <div>{expiryText}</div>
        <div className="server-list-sub">{billingText}</div>
      </div>
    </div>
  )
}, sameServerCardProps)

function ServerListHeader() {
  return (
    <div className="server-list-row is-header" aria-hidden="true">
      <span>节点</span>
      <span>CPU</span>
      <span>内存</span>
      <span>存储</span>
      <span>月流量</span>
      <span>实时网速</span>
      <span className="server-list-health">延迟 · 丢包（12 小时）</span>
      <span className="server-list-expiry">剩余 · 账单</span>
    </div>
  )
}

function ListMeter({ className, label, percent, text, compactText }: { className: string; label: string; percent: number | null | undefined; text: string; compactText?: string }) {
  return (
    <div className={`server-list-cell server-list-meter ${className}`}>
      <div className="server-list-meter__text">
        <span className="server-list-meter__label">{label}</span>
        <span className={compactText === undefined ? undefined : 'server-list-meter__full'}>{text}</span>
        {compactText !== undefined && <span className="server-list-meter__compact">{compactText}</span>}
      </div>
      <UsageTrack label={label} percent={percent} />
    </div>
  )
}

function ArrowUpIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6.5 10.5 12 5l5.5 5.5" /></svg>
}

function ArrowDownIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 5v14M6.5 13.5 12 19l5.5-5.5" /></svg>
}
