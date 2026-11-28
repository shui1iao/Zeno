import { useEffect, useRef, useState } from 'react'
import { fetchSummary, subscribeSummary, type SummaryData } from '../api/publicClient'
import { detailHttpFallbackDelayMs } from '../lib/detailTiming'
import { flushScheduledSummaryWrite, loadStoredSummary, scheduleRememberSummary } from '../lib/summaryCache'
import { startResilientLiveData } from '../lib/resilientLive'
import { sumValues } from '../lib/number'
import type { HomeCardNode } from '../types'

export type SummaryLoadState =
  | { kind: 'loading' }
  | { kind: 'ready'; data: SummaryData }
  | { kind: 'error'; message: string }

export type HomeRealtimeSnapshot = {
  nodes: HomeCardNode[]
  upSpeed: number
  downSpeed: number
}

const homeRealtimeSnapshotIntervalMs = 2000
const homeRealtimeSnapshotFrameToleranceMs = 150
const homeRealtimeStartupSyncMs = 1000

export function homeRealtimeSnapshotForNodes(nodes: HomeCardNode[]): HomeRealtimeSnapshot {
  return {
    nodes,
    upSpeed: sumValues(nodes.map((node) => node.netOutSpeedBps)),
    downSpeed: sumValues(nodes.map((node) => node.netInSpeedBps)),
  }
}

export function shouldRefreshHomeRealtimeSnapshot(lastUpdatedAt: number | null, now: number, mountedAt = now): boolean {
  return lastUpdatedAt === null || now - mountedAt <= homeRealtimeStartupSyncMs || now - lastUpdatedAt >= homeRealtimeSnapshotIntervalMs - homeRealtimeSnapshotFrameToleranceMs
}

function monotonicNowMs(): number {
  return typeof performance !== 'undefined' ? performance.now() : Date.now()
}

export function useSummaryController() {
  const initialSummary = loadStoredSummary()
  const [state, setState] = useState<SummaryLoadState>(() => initialSummary ? { kind: 'ready', data: initialSummary.data } : { kind: 'loading' })
  const summaryRef = useRef<SummaryData | null>(initialSummary?.data ?? null)
  const homeRealtimeMountedAtRef = useRef(monotonicNowMs())
  const homeRealtimeLastUpdatedAtRef = useRef<number | null>(null)
  const [homeRealtimeSnapshot, setHomeRealtimeSnapshot] = useState<HomeRealtimeSnapshot | null>(() => state.kind === 'ready' ? homeRealtimeSnapshotForNodes(state.data.nodes) : null)

  useEffect(() => {
    let cancelled = false
    const applySummaryData = (data: SummaryData) => {
      scheduleRememberSummary(data, Date.now())
      summaryRef.current = data
      if (cancelled) return
      const now = monotonicNowMs()
      if (shouldRefreshHomeRealtimeSnapshot(homeRealtimeLastUpdatedAtRef.current, now, homeRealtimeMountedAtRef.current)) {
        homeRealtimeLastUpdatedAtRef.current = now
        setHomeRealtimeSnapshot(homeRealtimeSnapshotForNodes(data.nodes))
      }
      setState({ kind: 'ready', data })
    }
    const stopLiveSummary = startResilientLiveData<SummaryData>({
      subscribe: subscribeSummary,
      fetch: fetchSummary,
      applyData: applySummaryData,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const message = error instanceof Error ? error.message : 'summary request failed'
        setState((current) => current.kind === 'ready' ? current : { kind: 'error', message })
      },
    })
    return () => {
      cancelled = true
      stopLiveSummary()
      flushScheduledSummaryWrite()
    }
  }, [])

  return { state, summaryRef, homeRealtimeSnapshot }
}
