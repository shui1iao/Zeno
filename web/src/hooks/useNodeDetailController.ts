import { useEffect, useRef, useState } from 'react'
import { fetchNodeLatency, fetchNodeState, peekPrefetchedNodeLatency, peekPrefetchedNodeState, prefetchNodeLatency, prefetchNodeState, subscribeNodeLatency, subscribeNodeState, type NodeLatencyData, type NodeStateData, type SummaryData } from '../api/publicClient'
import { captureAdminTokenIdentity, type AdminTokenIdentity } from '../lib/adminToken'
import { DetailMemoryCache, loadCachedDetailData, nodeLatencyCachePrefix, nodeStateCachePrefix, rememberDetailData } from '../lib/detailCache'
import { detailHttpFallbackDelayMs } from '../lib/detailTiming'
import { coerceHistoryRange, isHTTPUnauthorizedError, rangeRequiresAdmin } from '../lib/historyRange'
import { startResilientLiveData } from '../lib/resilientLive'
import type { StatePoint } from '../types'

export type LatencyLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeLatencyData }
  | { kind: 'error'; message: string }

export type StateHistoryLoadState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; data: NodeStateData }
  | { kind: 'error'; message: string }

const detailSnapshotKeyLimit = 12

export function shouldApplyNodeLatencySnapshot(seen: Map<string, string>, cacheKey: string, snapshotKey?: string): boolean {
  if (!snapshotKey) return true
  if (seen.get(cacheKey) === snapshotKey) return false
  seen.delete(cacheKey)
  seen.set(cacheKey, snapshotKey)
  while (seen.size > detailSnapshotKeyLimit) {
    const oldest = seen.keys().next().value as string | undefined
    if (oldest === undefined) break
    seen.delete(oldest)
  }
  return true
}

function validateNodeLatencyData(value: unknown): NodeLatencyData | null {
  const data = value as Partial<NodeLatencyData> | null
  if (!data || typeof data.nodeId !== 'string' || typeof data.range !== 'string' || !Array.isArray(data.points)) return null
  return data as NodeLatencyData
}

function validateNodeStateData(value: unknown): NodeStateData | null {
  const data = value as Partial<NodeStateData> | null
  if (!data || typeof data.nodeId !== 'string' || typeof data.range !== 'string' || !Array.isArray(data.points)) return null
  return data as NodeStateData
}

export function seedNodeLatencyFromSummary(summary: SummaryData | null, nodeId: string, range: string): NodeLatencyData | null {
  const node = summary?.nodes.find((item) => item.id === nodeId)
  if (!node) return null
  const summaries = node.latencySummaries && node.latencySummaries.length > 0 ? node.latencySummaries : node.latencySummary ? [node.latencySummary] : []
  if (summaries.length === 0) return null
  const primary = node.latencySummary
  const hourlyPreview = primary?.hourlyHistory?.flatMap((item) => {
    if (item.startedAt.trim() === '') return []
    return [{
      ts: item.startedAt,
      targetId: primary.targetId,
      targetName: primary.targetName,
      medianMs: item.latencyMs,
      avgMs: item.latencyMs,
      lossPercent: item.lossPercent ?? 0,
    }]
  }) ?? []
  // Seed every target in the summary's display order before appending the
  // primary target's preview history. The full history payload uses the same
  // display order, so the monitor tiles no longer reorder after the request
  // replaces this lightweight seed.
  const latestPoints = summaries.map((item) => ({
    ts: item.updatedAt || new Date().toISOString(),
    targetId: item.targetId,
    targetName: item.targetName,
    medianMs: item.medianMs,
    avgMs: item.avgMs ?? null,
    lossPercent: item.lossPercent ?? 0,
  }))
  return {
    nodeId,
    range,
    points: [...latestPoints, ...hourlyPreview],
  }
}

function seedNodeStateFromSummary(summary: SummaryData | null, nodeId: string, range: string): NodeStateData | null {
  const node = summary?.nodes.find((item) => item.id === nodeId)
  if (!node) return null
  const point: StatePoint = {
    ts: new Date().toISOString(),
    cpuPercent: node.cpuPercent,
    load1: node.load1 ?? null,
    load5: node.load5 ?? null,
    load15: node.load15 ?? null,
    memoryUsedBytes: node.memoryUsedBytes,
    memoryTotalBytes: node.memoryTotalBytes,
    swapUsedBytes: null,
    swapTotalBytes: null,
    diskUsedBytes: node.diskUsedBytes,
    diskTotalBytes: node.diskTotalBytes,
    netInTotalBytes: node.netInTotalBytes,
    netOutTotalBytes: node.netOutTotalBytes,
    netInSpeedBps: node.netInSpeedBps,
    netOutSpeedBps: node.netOutSpeedBps,
    processCount: null,
    tcpConnectionCount: null,
    udpConnectionCount: null,
    uptimeSeconds: node.uptimeSeconds ?? null,
  }
  return { nodeId, range, points: [point] }
}

interface NodeDetailControllerOptions {
  nodeId: string | null
  summary: SummaryData | null
  adminToken: string
  expireAdminSession: (identity: AdminTokenIdentity) => boolean
}

export function useNodeDetailController({ nodeId, summary, adminToken, expireAdminSession }: NodeDetailControllerOptions) {
  const [nodeLatencyRange, setNodeLatencyRange] = useState('1d')
  const [stateRange, setStateRange] = useState('1d')
  const [latencyState, setLatencyState] = useState<LatencyLoadState>(() => {
    if (nodeId === null) return { kind: 'idle' }
    const seeded = seedNodeLatencyFromSummary(summary, nodeId, '1d')
    return seeded ? { kind: 'ready', data: seeded } : { kind: 'idle' }
  })
  const [stateHistoryState, setStateHistoryState] = useState<StateHistoryLoadState>(() => {
    if (nodeId === null) return { kind: 'idle' }
    const seeded = seedNodeStateFromSummary(summary, nodeId, '1d')
    return seeded ? { kind: 'ready', data: seeded } : { kind: 'idle' }
  })
  const nodeLatencyCacheRef = useRef(new DetailMemoryCache<NodeLatencyData>())
  const nodeStateCacheRef = useRef(new DetailMemoryCache<NodeStateData>())
  const nodeLatencySnapshotRef = useRef(new Map<string, string>())

  useEffect(() => {
    const hasToken = adminToken !== ''
    const nextNodeRange = coerceHistoryRange(nodeLatencyRange, hasToken, '1d')
    const nextStateRange = coerceHistoryRange(stateRange, hasToken, '1d')
    if (nextNodeRange !== nodeLatencyRange) setNodeLatencyRange(nextNodeRange)
    if (nextStateRange !== stateRange) setStateRange(nextStateRange)
  }, [adminToken, nodeLatencyRange, stateRange])

  useEffect(() => {
    if (nodeId === null) {
      setLatencyState({ kind: 'idle' })
      return
    }
    let cancelled = false
    const cacheKey = `${nodeId}:${nodeLatencyRange}`
    const memoryCached = nodeLatencyCacheRef.current.getCached(cacheKey)
    const sessionCached = memoryCached ? null : loadCachedDetailData(nodeLatencyCachePrefix, nodeId, nodeLatencyRange, validateNodeLatencyData)
    const cached = memoryCached?.data ?? sessionCached?.data ?? null
    const requestToken = rangeRequiresAdmin(nodeLatencyRange) ? adminToken : undefined
    const seeded = cached ?? peekPrefetchedNodeLatency(nodeId, nodeLatencyRange, requestToken) ?? seedNodeLatencyFromSummary(summary, nodeId, nodeLatencyRange)
    if (sessionCached) nodeLatencyCacheRef.current.set(cacheKey, sessionCached.data, sessionCached.storedAt)
    if (cached?.snapshotKey) shouldApplyNodeLatencySnapshot(nodeLatencySnapshotRef.current, cacheKey, cached.snapshotKey)
    if (seeded) setLatencyState({ kind: 'ready', data: seeded })
    else setLatencyState((current) => current.kind === 'ready' && current.data.nodeId === nodeId ? current : { kind: 'loading' })
    if ((memoryCached ?? sessionCached)?.stale === false && rangeRequiresAdmin(nodeLatencyRange)) return
    const applyData = (data: NodeLatencyData) => {
      if (cancelled) return
      if (!shouldApplyNodeLatencySnapshot(nodeLatencySnapshotRef.current, cacheKey, data.snapshotKey)) return
      nodeLatencyCacheRef.current.set(cacheKey, data)
      rememberDetailData(nodeLatencyCachePrefix, nodeId, nodeLatencyRange, data)
      setLatencyState({ kind: 'ready', data })
    }
    const useLiveStream = !rangeRequiresAdmin(nodeLatencyRange)
    const requestTokenIdentity = requestToken ? captureAdminTokenIdentity(requestToken) : null
    const stop = startResilientLiveData<NodeLatencyData>({
      subscribe: useLiveStream ? (onData, onError, onStatus) => subscribeNodeLatency(nodeId, nodeLatencyRange, onData, onError, onStatus) : null,
      fetch: (signal) => fetchNodeLatency(nodeId, nodeLatencyRange, requestToken, signal),
      applyData,
      // Cold-open the detail view over HTTP rather than waiting out the socket
      // handshake: the seeded single-point chart would otherwise sit on screen
      // for about a second and then visibly jump to the real series.
      fetchImmediately: true,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const unauthorized = isHTTPUnauthorizedError(error)
        if (unauthorized && requestTokenIdentity && !expireAdminSession(requestTokenIdentity)) return
        setLatencyState((current) => current.kind === 'ready' ? current : { kind: 'error', message: unauthorized ? '登录已过期，请重新登录。' : error instanceof Error ? error.message : 'latency request failed' })
      },
    })
    return () => {
      cancelled = true
      stop()
    }
  }, [nodeId, nodeLatencyRange, adminToken, expireAdminSession])

  useEffect(() => {
    if (nodeId === null) {
      setStateHistoryState({ kind: 'idle' })
      return
    }
    let cancelled = false
    const cacheKey = `${nodeId}:${stateRange}`
    const memoryCached = nodeStateCacheRef.current.getCached(cacheKey)
    const sessionCached = memoryCached ? null : loadCachedDetailData(nodeStateCachePrefix, nodeId, stateRange, validateNodeStateData)
    const cached = memoryCached?.data ?? sessionCached?.data ?? null
    const requestToken = rangeRequiresAdmin(stateRange) ? adminToken : undefined
    const seeded = cached ?? peekPrefetchedNodeState(nodeId, stateRange, requestToken) ?? seedNodeStateFromSummary(summary, nodeId, stateRange)
    if (sessionCached) nodeStateCacheRef.current.set(cacheKey, sessionCached.data, sessionCached.storedAt)
    setStateHistoryState(seeded ? { kind: 'ready', data: seeded } : { kind: 'loading' })
    if ((memoryCached ?? sessionCached)?.stale === false && rangeRequiresAdmin(stateRange)) return
    const applyData = (data: NodeStateData) => {
      nodeStateCacheRef.current.set(cacheKey, data)
      rememberDetailData(nodeStateCachePrefix, nodeId, stateRange, data)
      if (!cancelled) setStateHistoryState({ kind: 'ready', data })
    }
    const useLiveStream = !rangeRequiresAdmin(stateRange)
    const requestTokenIdentity = requestToken ? captureAdminTokenIdentity(requestToken) : null
    const stop = startResilientLiveData<NodeStateData>({
      subscribe: useLiveStream ? (onData, onError, onStatus) => subscribeNodeState(nodeId, stateRange, onData, onError, onStatus) : null,
      fetch: (signal) => fetchNodeState(nodeId, stateRange, requestToken, signal),
      applyData,
      // Cold-open the detail view over HTTP rather than waiting out the socket
      // handshake: the seeded single-point chart would otherwise sit on screen
      // for about a second and then visibly jump to the real series.
      fetchImmediately: true,
      initialFallbackDelayMs: detailHttpFallbackDelayMs,
      onError: (error) => {
        if (cancelled) return
        const unauthorized = isHTTPUnauthorizedError(error)
        if (unauthorized && requestTokenIdentity && !expireAdminSession(requestTokenIdentity)) return
        setStateHistoryState((current) => current.kind === 'ready' ? current : { kind: 'error', message: unauthorized ? '登录已过期，请重新登录。' : error instanceof Error ? error.message : 'state request failed' })
      },
    })
    return () => {
      cancelled = true
      stop()
    }
  }, [nodeId, stateRange, adminToken, expireAdminSession])

  useEffect(() => {
    if (nodeId === null || adminToken === '') return
    let cancelled = false
    const prefetchExtendedHistory = async () => {
      for (const range of ['7d', '30d']) {
        if (cancelled) return
        await prefetchNodeLatency(nodeId, range, adminToken).catch(() => {})
        if (cancelled) return
        await prefetchNodeState(nodeId, range, adminToken).catch(() => {})
      }
    }
    const timer = setTimeout(() => {
      void prefetchExtendedHistory()
    }, 0)
    return () => {
      cancelled = true
      clearTimeout(timer)
    }
  }, [nodeId, adminToken])

  useEffect(() => {
    if (nodeId === null || summary === null) return
    const latencySeed = seedNodeLatencyFromSummary(summary, nodeId, nodeLatencyRange)
    if (latencySeed) setLatencyState((current) => current.kind === 'loading' || current.kind === 'idle' ? { kind: 'ready', data: latencySeed } : current)
    const stateSeed = seedNodeStateFromSummary(summary, nodeId, stateRange)
    if (stateSeed) setStateHistoryState((current) => current.kind === 'loading' || current.kind === 'idle' ? { kind: 'ready', data: stateSeed } : current)
  }, [summary, nodeId, nodeLatencyRange, stateRange])

  return {
    nodeLatencyRange,
    stateRange,
    latencyState,
    stateHistoryState,
    setNodeLatencyRange,
    setStateRange,
    resetNodeRanges: () => {
      setNodeLatencyRange('1d')
      setStateRange('1d')
    },
  }
}
