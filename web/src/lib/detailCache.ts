export type CachedDetail<T> = {
  data: T
  storedAt: number
  stale: boolean
}

export const nodeLatencyCachePrefix = 'zeno_node_latency_cache_v1'
export const nodeStateCachePrefix = 'zeno_node_state_cache_v1'
export const detailCacheFreshTtlMs = 30 * 1000
export const detailCacheMaxAgeMs = 5 * 60 * 1000
export const detailCacheMaxBytes = 700_000
export const detailMemoryCacheMaxEntries = 12
export const detailSessionCacheMaxEntries = 24

// The retired zeno_service_latency_cache_v1 entries are still recognised so old
// session data from the removed single-target page is pruned with the rest.
const detailCachePrefixes = [nodeLatencyCachePrefix, nodeStateCachePrefix, 'zeno_service_latency_cache_v1'] as const

type StoredDetailEnvelope = {
  storedAt: number
  lastAccessedAt: number
  data: unknown
}

type StoredDetailEntry = {
  key: string
  storedAt: number
  lastAccessedAt: number
}

type MemoryDetailEntry<T> = {
  data: T
  storedAt: number
}

export class DetailMemoryCache<T> {
  private readonly entries = new Map<string, MemoryDetailEntry<T>>()
  private readonly maxEntries: number
  private readonly maxAgeMs: number

  constructor(maxEntries = detailMemoryCacheMaxEntries, maxAgeMs = detailCacheMaxAgeMs) {
    this.maxEntries = Math.max(1, Math.floor(maxEntries))
    this.maxAgeMs = Math.max(0, maxAgeMs)
  }

  get size(): number {
    return this.entries.size
  }

  get(key: string, now = Date.now()): T | undefined {
    return this.getCached(key, now)?.data
  }

  getCached(key: string, now = Date.now()): CachedDetail<T> | null {
    const entry = this.entries.get(key)
    if (!entry) return null
    if (isExpired(entry.storedAt, now, this.maxAgeMs)) {
      this.entries.delete(key)
      return null
    }

    // Map insertion order is the LRU order. Move successful reads to the end.
    this.entries.delete(key)
    this.entries.set(key, entry)
    return { data: entry.data, storedAt: entry.storedAt, stale: now - entry.storedAt > detailCacheFreshTtlMs }
  }

  set(key: string, data: T, storedAt = Date.now()): void {
    this.entries.delete(key)
    this.entries.set(key, { data, storedAt })
    while (this.entries.size > this.maxEntries) {
      const leastRecentlyUsedKey = this.entries.keys().next().value as string | undefined
      if (leastRecentlyUsedKey === undefined) break
      this.entries.delete(leastRecentlyUsedKey)
    }
  }

  clear(): void {
    this.entries.clear()
  }
}

export function detailCacheKey(prefix: string, resourceId: string, range: string): string {
  return `${prefix}:${resourceId}:${range}`
}

export function loadCachedDetailData<T>(prefix: string, resourceId: string, range: string, validate: (value: unknown) => T | null, now = Date.now()): CachedDetail<T> | null {
  const store = getSessionStorage()
  if (!store) return null

  const key = detailCacheKey(prefix, resourceId, range)
  let raw: string | null
  try {
    raw = store.getItem(key)
  } catch {
    return null
  }
  if (!raw) return null

  const parsed = parseStoredDetail(raw)
  if (!parsed || isExpired(parsed.storedAt, now, detailCacheMaxAgeMs)) {
    removeStorageItem(store, key)
    return null
  }

  let data: T | null
  try {
    data = validate(parsed.data)
  } catch {
    data = null
  }
  if (data === null) {
    removeStorageItem(store, key)
    return null
  }

  // Persist the read recency when possible. A failed touch must never make a
  // valid cached response unavailable to the caller.
  try {
    store.setItem(key, JSON.stringify({ ...parsed, lastAccessedAt: now }))
  } catch {}

  return { data, storedAt: parsed.storedAt, stale: now - parsed.storedAt > detailCacheFreshTtlMs }
}

export function rememberDetailData(prefix: string, resourceId: string, range: string, data: unknown, now = Date.now()): void {
  const store = getSessionStorage()
  if (!store) return

  let payload: string
  try {
    payload = JSON.stringify({ storedAt: now, lastAccessedAt: now, data })
  } catch {
    return
  }
  if (payload.length > detailCacheMaxBytes) return

  const key = detailCacheKey(prefix, resourceId, range)
  const entries = inspectStoredDetailEntries(store, now)
  const existing = entries.some((entry) => entry.key === key)
  const evictionCandidates = entries
    .filter((entry) => entry.key !== key)
    .sort((left, right) => left.lastAccessedAt - right.lastAccessedAt || left.storedAt - right.storedAt || left.key.localeCompare(right.key))

  let resultingEntryCount = entries.length + (existing ? 0 : 1)
  while (resultingEntryCount > detailSessionCacheMaxEntries && evictionCandidates.length > 0) {
    removeStorageItem(store, evictionCandidates.shift()!.key)
    resultingEntryCount -= 1
  }

  try {
    store.setItem(key, payload)
    return
  } catch (error) {
    if (!isQuotaExceededError(error)) return
  }

  // Quota can be smaller than the configured entry limit. Retry after
  // evicting only our own least-recently-used detail entries, then silently
  // degrade if storage remains unavailable.
  while (evictionCandidates.length > 0) {
    removeStorageItem(store, evictionCandidates.shift()!.key)
    try {
      store.setItem(key, payload)
      return
    } catch (error) {
      if (!isQuotaExceededError(error)) return
    }
  }
}

function getSessionStorage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    return window.sessionStorage ?? null
  } catch {
    return null
  }
}

function parseStoredDetail(raw: string): StoredDetailEnvelope | null {
  try {
    const parsed = JSON.parse(raw) as Partial<StoredDetailEnvelope> | null
    if (!parsed || typeof parsed !== 'object') return null
    const storedAt = Number(parsed.storedAt ?? 0)
    if (!Number.isFinite(storedAt) || storedAt <= 0 || !('data' in parsed)) return null
    const rawLastAccessedAt = Number(parsed.lastAccessedAt ?? storedAt)
    const lastAccessedAt = Number.isFinite(rawLastAccessedAt) && rawLastAccessedAt > 0 ? rawLastAccessedAt : storedAt
    return { storedAt, lastAccessedAt, data: parsed.data }
  } catch {
    return null
  }
}

function inspectStoredDetailEntries(store: Storage, now: number): StoredDetailEntry[] {
  const entries: StoredDetailEntry[] = []
  for (const key of storageKeys(store)) {
    if (!isManagedDetailCacheKey(key)) continue
    let raw: string | null
    try {
      raw = store.getItem(key)
    } catch {
      continue
    }
    const parsed = raw ? parseStoredDetail(raw) : null
    if (!parsed || isExpired(parsed.storedAt, now, detailCacheMaxAgeMs)) {
      removeStorageItem(store, key)
      continue
    }
    entries.push({ key, storedAt: parsed.storedAt, lastAccessedAt: parsed.lastAccessedAt })
  }
  return entries
}

function storageKeys(store: Storage): string[] {
  const keys: string[] = []
  try {
    for (let index = 0; index < store.length; index += 1) {
      const key = store.key(index)
      if (key !== null) keys.push(key)
    }
  } catch {
    return []
  }
  return keys
}

function isManagedDetailCacheKey(key: string): boolean {
  return detailCachePrefixes.some((prefix) => key.startsWith(`${prefix}:`))
}

function isExpired(storedAt: number, now: number, maxAgeMs: number): boolean {
  return !Number.isFinite(storedAt) || storedAt <= 0 || now - storedAt > maxAgeMs
}

function removeStorageItem(store: Storage, key: string): void {
  try {
    store.removeItem(key)
  } catch {}
}

function isQuotaExceededError(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const candidate = error as { name?: unknown; code?: unknown }
  return candidate.name === 'QuotaExceededError'
    || candidate.name === 'NS_ERROR_DOM_QUOTA_REACHED'
    || candidate.code === 22
    || candidate.code === 1014
}
