import type { ReactNode } from 'react'
import type { SummaryData } from '../api/publicClient'
import { useNodeDetailController } from '../hooks/useNodeDetailController'
import type { AdminTokenIdentity } from '../lib/adminToken'
import type { HomeCardNode, LatencyPoint } from '../types'
import { LatencyDetail } from './LatencyDetail'
import '../styles/detail.css'

export interface NodeDetailRouteProps {
  node: HomeCardNode
  summary: SummaryData | null
  adminToken: string
  expireAdminSession: (identity: AdminTokenIdentity) => boolean
  onBack: () => void
  topHeader: ReactNode
}

function summaryLatencyPoints(node: HomeCardNode): LatencyPoint[] {
  return (node.latencySummaries ?? [])
    .filter((summary) => summary.updatedAt)
    .map((summary) => ({
      ts: summary.updatedAt,
      targetId: summary.targetId,
      targetName: summary.targetName,
      medianMs: summary.medianMs,
      avgMs: summary.avgMs,
      lossPercent: summary.lossPercent ?? 0,
    }))
}

export function NodeDetailRoute({ node, summary, adminToken, expireAdminSession, onBack, topHeader }: NodeDetailRouteProps) {
  const {
    nodeLatencyRange,
    stateRange,
    latencyState,
    stateHistoryState,
    setNodeLatencyRange,
    setStateRange,
  } = useNodeDetailController({
    nodeId: node.id,
    summary,
    adminToken,
    expireAdminSession,
  })

  return (
    <LatencyDetail
      node={node}
      points={latencyState.kind === 'ready' ? latencyState.data.points : summaryLatencyPoints(node)}
      statePoints={stateHistoryState.kind === 'ready' ? stateHistoryState.data.points : []}
      range={nodeLatencyRange}
      stateRange={stateRange}
      loading={latencyState.kind === 'loading'}
      error={latencyState.kind === 'error' ? latencyState.message : undefined}
      stateLoading={stateHistoryState.kind === 'loading'}
      stateError={stateHistoryState.kind === 'error' ? stateHistoryState.message : undefined}
      canUseExtendedRanges={adminToken !== ''}
      onBack={onBack}
      onRangeChange={setNodeLatencyRange}
      onStateRangeChange={setStateRange}
      topHeader={topHeader}
      targetOrder={summary?.services.map((service) => service.id)}
    />
  )
}
