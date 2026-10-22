import type { AdminSettings } from '../types'
import { adminCookieSessionMarker, captureAdminTokenIdentity } from '../lib/adminToken'
import { normalizeNodeLatency, normalizeNodeState, normalizeSettings, normalizeSummary } from './publicNormalizers'
import type { ApiLatencyResponse, ApiSettings, ApiStateResponse, ApiSummaryResponse, NodeLatencyData, NodeStateData, SummaryData } from './apiTypes'
export type { ApiLatencyResponse, ApiStateResponse, ApiSummaryResponse, NodeLatencyData, NodeStateData, SummaryData } from './apiTypes'
export { nodeLatencySnapshotKey, normalizeNodeLatency, normalizeNodeState, normalizeSettings, normalizeSummary } from './publicNormalizers'

export type LiveWebSocketStatus = 'connecting' | 'open' | 'reconnecting' | 'closed'

export async function fetchPublicSettings(): Promise<AdminSettings> {
  const response = await fetch('/api/public/v1/settings', { headers: { Accept: 'application/json' } })
  if (!response.ok) {
    throw new Error(`settings request failed: ${response.status}`)
  }
  return normalizeSettings(await response.json() as ApiSettings)
}

export async function fetchSummary(signal?: AbortSignal): Promise<SummaryData> {
  const response = await fetch('/api/public/v1/summary', { signal, headers: { Accept: 'application/json' } })
  if (!response.ok) {
    throw new Error(`summary request failed: ${response.status}`)
  }
  return normalizeSummary(await response.json() as ApiSummaryResponse)
}

function liveWebSocketURL(path: string): string {
  const baseURL = typeof window === 'undefined' ? 'http://localhost/' : window.location.href
  const url = new URL(path, baseURL)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

function subscribeLiveWebSocket<T>(path: string, normalize: (payload: unknown) => T, onData: (data: T) => void, onError?: (error: Error) => void, onStatus?: (status: LiveWebSocketStatus) => void): (() => void) | null {
  if (typeof WebSocket === 'undefined') return null
  let closedByClient = false
  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let reconnectAttempts = 0

  const clearReconnectTimer = () => {
    if (reconnectTimer !== null) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
  }

  const connect = () => {
    if (closedByClient) return
    onStatus?.(reconnectAttempts > 0 ? 'reconnecting' : 'connecting')
    socket = new WebSocket(liveWebSocketURL(path))
    socket.onopen = () => {
      onStatus?.('open')
    }
    socket.onmessage = (event) => {
      try {
        if (typeof event.data !== 'string') throw new Error('live websocket message must be text')
        reconnectAttempts = 0
        onData(normalize(JSON.parse(event.data) as unknown))
      } catch (error) {
        onError?.(error instanceof Error ? error : new Error('live websocket parse failed'))
      }
    }
    socket.onerror = () => {
      socket?.close()
    }
    socket.onclose = () => {
      if (closedByClient) return
      reconnectAttempts += 1
      onStatus?.('reconnecting')
      clearReconnectTimer()
      reconnectTimer = setTimeout(connect, Math.min(1000 + reconnectAttempts * 250, 30_000))
    }
  }

  connect()
  return () => {
    closedByClient = true
    clearReconnectTimer()
    socket?.close()
    socket = null
  }
}

export function subscribeSummary(onSummary: (summary: SummaryData) => void, onError?: (error: Error) => void, onStatus?: (status: LiveWebSocketStatus) => void): (() => void) | null {
  return subscribeLiveWebSocket('/api/public/v1/summary/ws', (payload) => normalizeSummary(payload as ApiSummaryResponse), onSummary, onError, onStatus)
}

export function subscribeNodeLatency(nodeId: string, range: string, onLatency: (latency: NodeLatencyData) => void, onError?: (error: Error) => void, onStatus?: (status: LiveWebSocketStatus) => void): (() => void) | null {
  return subscribeLiveWebSocket(`/api/public/v1/nodes/${encodeURIComponent(nodeId)}/latency/ws?range=${encodeURIComponent(range)}`, (payload) => normalizeNodeLatency(payload as ApiLatencyResponse), onLatency, onError, onStatus)
}

export function subscribeNodeState(nodeId: string, range: string, onState: (state: NodeStateData) => void, onError?: (error: Error) => void, onStatus?: (status: LiveWebSocketStatus) => void): (() => void) | null {
  return subscribeLiveWebSocket(`/api/public/v1/nodes/${encodeURIComponent(nodeId)}/state/ws?range=${encodeURIComponent(range)}`, (payload) => normalizeNodeState(payload as ApiStateResponse), onState, onError, onStatus)
}

function optionalAdminHeaders(adminToken?: string): HeadersInit {
  return adminToken && adminToken !== adminCookieSessionMarker ? { Accept: 'application/json', 'X-Admin-Token': adminToken } : { Accept: 'application/json' }
}

const nodeDetailPrefetchRetentionMs = 60 * 1000
const nodeDetailPrefetchTimeoutMs = 10_000
const nodeDetailPrefetchMaxEntries = 4
interface NodeDetailPrefetch<T> {
  promise: Promise<T>
  consumed: boolean
  data?: T
}
const nodeLatencyPrefetches = new Map<string, NodeDetailPrefetch<NodeLatencyData>>()
const nodeStatePrefetches = new Map<string, NodeDetailPrefetch<NodeStateData>>()

function nodeDetailPrefetchKey(nodeId: string, range: string, adminToken?: string): string {
  const credential = adminToken === adminCookieSessionMarker
    ? `cookie:${captureAdminTokenIdentity(adminToken).generation}`
    : adminToken ?? null
  return JSON.stringify([nodeId, range, credential])
}

function touchNodeDetailPrefetch<T>(entries: Map<string, NodeDetailPrefetch<T>>, key: string, entry: NodeDetailPrefetch<T>): void {
  entries.delete(key)
  entries.set(key, entry)
}

function pruneNodeDetailPrefetches<T>(entries: Map<string, NodeDetailPrefetch<T>>): void {
  while (entries.size > nodeDetailPrefetchMaxEntries) {
    const oldestKey = entries.keys().next().value as string | undefined
    if (oldestKey === undefined) break
    entries.delete(oldestKey)
  }
}

async function requestNodeLatency(nodeId: string, range: string, adminToken?: string, signal?: AbortSignal): Promise<NodeLatencyData> {
  const response = await fetch(`/api/public/v1/nodes/${encodeURIComponent(nodeId)}/latency?range=${encodeURIComponent(range)}`, { signal, headers: optionalAdminHeaders(adminToken) })
  if (!response.ok) {
    throw new Error(`latency request failed: ${response.status}`)
  }
  return normalizeNodeLatency(await response.json() as ApiLatencyResponse)
}

function awaitPrefetchedNodeDetail<T>(prefetch: Promise<T>, signal?: AbortSignal): Promise<T> {
  if (!signal) return prefetch
  if (signal.aborted) return Promise.reject(signal.reason ?? new DOMException('The operation was aborted.', 'AbortError'))
  return new Promise((resolve, reject) => {
    const abort = () => reject(signal.reason ?? new DOMException('The operation was aborted.', 'AbortError'))
    signal.addEventListener('abort', abort, { once: true })
    prefetch.then(
      (data) => {
        signal.removeEventListener('abort', abort)
        resolve(data)
      },
      (error: unknown) => {
        signal.removeEventListener('abort', abort)
        reject(error)
      },
    )
  })
}

function prefetchNodeDetail<T>(entries: Map<string, NodeDetailPrefetch<T>>, key: string, load: (signal: AbortSignal) => Promise<T>): Promise<T> {
  const existing = entries.get(key)
  if (existing) {
    touchNodeDetailPrefetch(entries, key, existing)
    return existing.promise
  }

  const controller = new AbortController()
  const request = load(controller.signal)
  const entry: NodeDetailPrefetch<T> = { promise: request, consumed: false }
  const timeoutId = setTimeout(() => controller.abort(), nodeDetailPrefetchTimeoutMs)
  entries.set(key, entry)
  pruneNodeDetailPrefetches(entries)
  request.then(
    (data) => {
      clearTimeout(timeoutId)
      entry.data = data
      setTimeout(() => {
        if (entries.get(key) === entry) entries.delete(key)
      }, nodeDetailPrefetchRetentionMs)
    },
    () => {
      clearTimeout(timeoutId)
      if (entries.get(key) === entry) entries.delete(key)
    },
  )
  return request
}

function peekPrefetchedNodeDetail<T>(entries: Map<string, NodeDetailPrefetch<T>>, key: string): T | null {
  const entry = entries.get(key)
  if (!entry?.data) return null
  touchNodeDetailPrefetch(entries, key, entry)
  return entry.data
}

function consumePrefetchedNodeDetail<T>(entries: Map<string, NodeDetailPrefetch<T>>, key: string): Promise<T> | null {
  const prefetched = entries.get(key)
  if (prefetched && !prefetched.consumed) {
    prefetched.consumed = true
    touchNodeDetailPrefetch(entries, key, prefetched)
    return prefetched.promise
  }
  return null
}

export function prefetchNodeLatency(nodeId: string, range = '1d', adminToken?: string): Promise<NodeLatencyData> {
  const key = nodeDetailPrefetchKey(nodeId, range, adminToken)
  return prefetchNodeDetail(nodeLatencyPrefetches, key, (signal) => requestNodeLatency(nodeId, range, adminToken, signal))
}

export function peekPrefetchedNodeLatency(nodeId: string, range = '1d', adminToken?: string): NodeLatencyData | null {
  return peekPrefetchedNodeDetail(nodeLatencyPrefetches, nodeDetailPrefetchKey(nodeId, range, adminToken))
}

export async function fetchNodeLatency(nodeId: string, range = '1h', adminToken?: string, signal?: AbortSignal): Promise<NodeLatencyData> {
  const prefetched = consumePrefetchedNodeDetail(nodeLatencyPrefetches, nodeDetailPrefetchKey(nodeId, range, adminToken))
  if (prefetched) return awaitPrefetchedNodeDetail(prefetched, signal)
  return requestNodeLatency(nodeId, range, adminToken, signal)
}

async function requestNodeState(nodeId: string, range = '1h', adminToken?: string, signal?: AbortSignal): Promise<NodeStateData> {
  const response = await fetch(`/api/public/v1/nodes/${encodeURIComponent(nodeId)}/state?range=${encodeURIComponent(range)}`, { signal, headers: optionalAdminHeaders(adminToken) })
  if (!response.ok) {
    throw new Error(`state request failed: ${response.status}`)
  }
  return normalizeNodeState(await response.json() as ApiStateResponse)
}

export function prefetchNodeState(nodeId: string, range: string, adminToken?: string): Promise<NodeStateData> {
  const key = nodeDetailPrefetchKey(nodeId, range, adminToken)
  return prefetchNodeDetail(nodeStatePrefetches, key, (signal) => requestNodeState(nodeId, range, adminToken, signal))
}

export function peekPrefetchedNodeState(nodeId: string, range: string, adminToken?: string): NodeStateData | null {
  return peekPrefetchedNodeDetail(nodeStatePrefetches, nodeDetailPrefetchKey(nodeId, range, adminToken))
}

export async function fetchNodeState(nodeId: string, range = '1h', adminToken?: string, signal?: AbortSignal): Promise<NodeStateData> {
  const prefetched = consumePrefetchedNodeDetail(nodeStatePrefetches, nodeDetailPrefetchKey(nodeId, range, adminToken))
  if (prefetched) return awaitPrefetchedNodeDetail(prefetched, signal)
  return requestNodeState(nodeId, range, adminToken, signal)
}
