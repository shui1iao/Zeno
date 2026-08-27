import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchNodeLatency, fetchNodeState, fetchServiceLatency, nodeLatencySnapshotKey, normalizeSettings, normalizeNodeLatency, normalizeNodeState, normalizeServiceLatency, normalizeSummary, peekPrefetchedNodeLatency, peekPrefetchedNodeState, prefetchNodeLatency, prefetchNodeState } from './client'
import { clearStoredAdminToken, loadStoredAdminToken, rememberAdminToken } from '../lib/adminToken'

describe('normalizeSummary', () => {
  it('maps controller snake_case JSON into frontend camelCase models', () => {
    const summary = normalizeSummary({
      nodes: [
        {
          id: 'example-node-a',
          display_name: 'Example Node A',
          status: 'online',
          os: 'debian',
          os_version: '13',
          kernel: '6.12.0',
          virtualization: 'kvm',
          cpu_model: 'AMD EPYC',
          arch: 'aarch64',
          country_code: 'HK',
          subtitle: 'Hong Kong',
          cpu_cores: 2,
          expiry_label: '永 久',
          renewal_amount: 20,
          renewal_currency: 'USD',
          billing_cycle: '年',
          monthly_cost_cny: 11.75,
          cpu_percent: 12.5,
          memory_used_bytes: 100,
          memory_total_bytes: 200,
          disk_used_bytes: 300,
          disk_total_bytes: 400,
          boot_time: '2026-07-02T01:00:00Z',
          load1: 0.42,
          load5: 0.35,
          load15: 0.28,
          uptime_seconds: 3600,
          net_in_speed_bps: 1024,
          net_out_speed_bps: 2048,
          net_in_total_bytes: 4096,
          net_out_total_bytes: 8192,
          net_in_lifetime_bytes: 16384,
          net_out_lifetime_bytes: 32768,
          billing_mode: 'max',
          monthly_reset_day: 15,
          monthly_period_start: '2026-06-15',
          monthly_period_end: '2026-07-14',
          monthly_billable_bytes: 1000,
          monthly_quota_bytes: 2000,
          latency_summary: {
            target_id: 'google',
            target_name: 'Google',
            median_ms: 1.2,
            avg_ms: 1.4,
            loss_percent: 0,
            updated_at: '2026-07-02T12:00:00Z',
          },
          latency_summaries: [
            {
              target_id: 'google',
              target_name: 'Google',
              median_ms: 1.2,
              avg_ms: 1.4,
              loss_percent: 0,
              updated_at: '2026-07-02T12:00:00Z',
            },
          ],
        },
      ],
      services: [
        { id: 'google', name: 'Google', type: 'http_get', assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      ],
      latency_points: [
        { ts: '2026-07-02T12:00:00Z', target_id: 'google', target_name: 'Google', median_ms: null, loss_percent: 100 },
      ],
      exchange_rates: { CNY: 1, USD: 8, EUR: 9 },
    })

    expect(summary.nodes[0].displayName).toBe('Example Node A')
    expect(summary.nodes[0].arch).toBe('aarch64')
    expect(summary.nodes[0].countryCode).toBe('HK')
    expect(summary.nodes[0].cpuCores).toBe(2)
    expect(summary.nodes[0].expiryLabel).toBe('永 久')
    expect(summary.nodes[0].renewalAmount).toBe(20)
    expect(summary.nodes[0].renewalCurrency).toBe('USD')
    expect(summary.nodes[0].billingCycle).toBe('年')
    expect(summary.nodes[0].monthlyCostCny).toBe(11.75)
    expect(summary.nodes[0].billingMode).toBe('max')
    expect(summary.nodes[0].monthlyResetDay).toBe(15)
    expect(summary.nodes[0].monthlyPeriodStart).toBe('2026-06-15')
    expect(summary.nodes[0].monthlyPeriodEnd).toBe('2026-07-14')
    expect(summary.nodes[0].monthlyBillableBytes).toBe(1000)
    expect(summary.nodes[0].latencySummary?.targetName).toBe('Google')
    expect(summary.nodes[0].latencySummaries?.[0].targetId).toBe('google')
    expect(summary.nodes[0].load1).toBe(0.42)
    expect(summary.nodes[0].load5).toBe(0.35)
    expect(summary.nodes[0].load15).toBe(0.28)
    expect(summary.nodes[0].uptimeSeconds).toBe(3600)
    expect(summary.nodes[0].netInLifetimeBytes).toBe(16384)
    expect(summary.nodes[0].netOutLifetimeBytes).toBe(32768)
    expect(summary.nodes[0].osVersion).toBe('13')
    expect(summary.nodes[0].kernel).toBe('6.12.0')
    expect(summary.nodes[0].virtualization).toBe('kvm')
    expect(summary.nodes[0].cpuModel).toBe('AMD EPYC')
    expect(summary.nodes[0].bootTime).toBe('2026-07-02T01:00:00Z')
    expect(summary.latencyPoints[0].targetId).toBe('google')
    expect(summary.latencyPoints[0].medianMs).toBeNull()
    expect(summary.latencyPoints[0].lossPercent).toBe(100)
    expect(summary.services[0].name).toBe('Google')
    expect(summary.services[0].reportingNodeCount).toBe(9)
    expect(summary.services[0].avgMs).toBeNull()
    expect(summary.exchangeRates).toEqual({ CNY: 1, USD: 8, EUR: 9 })
  })

  it('normalizes null collections from empty preview stores', () => {
    const summary = normalizeSummary({
      nodes: null,
      services: null,
      latency_points: null,
    })

    expect(summary.nodes).toEqual([])
    expect(summary.services).toEqual([])
    expect(summary.latencyPoints).toEqual([])
    expect(summary.exchangeRates).toEqual({ CNY: 1 })
  })
})

describe('normalizeServiceLatency', () => {
  it('maps service latency points into chart-compatible node series', () => {
    const data = normalizeServiceLatency({
      target: { id: 'google', name: 'Google', type: 'http_get', assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      range: '1d',
      points: [
        { ts: '2026-07-02T12:00:00Z', node_id: 'example-node-a', node_name: 'Example Node A', median_ms: 1.4, loss_percent: 0 },
      ],
    })

    expect(data.target.id).toBe('google')
    expect(data.target.assignedNodeCount).toBe(10)
    expect(data.points[0].targetId).toBe('example-node-a')
    expect(data.points[0].targetName).toBe('Example Node A')
  })

  it('expands compact Kulin-style node series into chart-compatible points', () => {
    const data = normalizeServiceLatency({
      target: { id: 'google', name: 'Google', type: 'http_get', assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      range: '1d',
      series: [
        {
          node_id: 'example-node-a',
          node_name: 'Example Node A',
          created_at: [Date.parse('2026-07-02T12:00:00Z')],
          median_ms: [1.3],
          avg_ms: [1.5],
          loss_percent: [0],
        },
      ],
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', targetId: 'example-node-a', targetName: 'Example Node A', medianMs: 1.3, avgMs: 1.5, lossPercent: 0 }),
    ])
  })

  it('expands service latency series with shared timestamps', () => {
    const data = normalizeServiceLatency({
      target: { id: 'google', name: 'Google', type: 'http_get', assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0, updated_at: '2026-07-02T12:00:00Z' },
      range: '1d',
      created_at: [Date.parse('2026-07-02T12:00:00Z'), Date.parse('2026-07-02T12:01:00Z')],
      series: [
        {
          node_id: 'example-node-a',
          node_name: 'Example Node A',
          avg_ms: [1.5, 2.5],
          loss_percent: [0, 1],
        },
      ],
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', targetId: 'example-node-a', targetName: 'Example Node A', avgMs: 1.5, lossPercent: 0 }),
      expect.objectContaining({ ts: '2026-07-02T12:01:00.000Z', targetId: 'example-node-a', targetName: 'Example Node A', avgMs: 2.5, lossPercent: 1 }),
    ])
  })
})

describe('fetchServiceLatency', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('fetches public service latency without admin credentials', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      target: { id: 'google', name: 'Google', type: 'http_get', address: 'https://www.google.com/generate_204', port: null, assigned_node_count: 10, reporting_node_count: 9, median_ms: 1.2, loss_percent: 0 },
      range: '7d',
      points: [],
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const data = await fetchServiceLatency('google', '7d')

    expect(data.target.name).toBe('Google')
    expect(data.range).toBe('7d')
    expect(fetchMock).toHaveBeenCalledWith('/api/public/v1/services/google/latency?range=7d', {
      headers: { Accept: 'application/json' },
    })
  })
})

describe('prefetchNodeLatency', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('reuses the route prefetch when the detail controller requests the same chart', async () => {
    let resolveFetch: ((response: Response) => void) | undefined
    const fetchMock = vi.fn(() => new Promise<Response>((resolve) => { resolveFetch = resolve }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const prefetched = prefetchNodeLatency('prefetched-node', '1d')
    const requested = fetchNodeLatency('prefetched-node', '1d')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/public/v1/nodes/prefetched-node/latency?range=1d', {
      signal: expect.any(AbortSignal),
      headers: { Accept: 'application/json' },
    })

    resolveFetch?.(new Response(JSON.stringify({ node_id: 'prefetched-node', range: '1d', points: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))

    await expect(prefetched).resolves.toEqual(expect.objectContaining({ nodeId: 'prefetched-node', range: '1d' }))
    await expect(requested).resolves.toEqual(expect.objectContaining({ nodeId: 'prefetched-node', range: '1d' }))
    expect(peekPrefetchedNodeLatency('prefetched-node', '1d')).toEqual(expect.objectContaining({ nodeId: 'prefetched-node', range: '1d' }))

    const reopened = fetchNodeLatency('prefetched-node', '1d')
    expect(fetchMock).toHaveBeenCalledTimes(2)
    resolveFetch?.(new Response(JSON.stringify({ node_id: 'prefetched-node', range: '1d', points: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    await expect(reopened).resolves.toEqual(expect.objectContaining({ nodeId: 'prefetched-node', range: '1d' }))
  })

  it('times out a stalled intent prefetch without pinning later detail requests to it', async () => {
    vi.useFakeTimers()
    const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
      if (fetchMock.mock.calls.length === 1) {
        return new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(init.signal?.reason), { once: true })
        })
      }
      return Promise.resolve(new Response(JSON.stringify({ node_id: 'timeout-node', range: '1d', points: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const stalled = prefetchNodeLatency('timeout-node', '1d')
    const rejected = expect(stalled).rejects.toMatchObject({ name: 'AbortError' })
    await vi.advanceTimersByTimeAsync(10_000)
    await rejected

    await expect(fetchNodeLatency('timeout-node', '1d')).resolves.toEqual(expect.objectContaining({ nodeId: 'timeout-node' }))
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})

describe('prefetchNodeState', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    clearStoredAdminToken()
    vi.restoreAllMocks()
  })

  it('reuses an authenticated extended-range prefetch on the first range switch', async () => {
    let resolveFetch: ((response: Response) => void) | undefined
    const fetchMock = vi.fn(() => new Promise<Response>((resolve) => { resolveFetch = resolve }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const prefetched = prefetchNodeState('prefetched-state-node', '7d', 'test-admin-token')
    const requested = fetchNodeState('prefetched-state-node', '7d', 'test-admin-token')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/public/v1/nodes/prefetched-state-node/state?range=7d', {
      signal: expect.any(AbortSignal),
      headers: { Accept: 'application/json', 'X-Admin-Token': 'test-admin-token' },
    })

    resolveFetch?.(new Response(JSON.stringify({ node_id: 'prefetched-state-node', range: '7d', points: [] }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))

    await expect(prefetched).resolves.toEqual(expect.objectContaining({ nodeId: 'prefetched-state-node', range: '7d' }))
    await expect(requested).resolves.toEqual(expect.objectContaining({ nodeId: 'prefetched-state-node', range: '7d' }))
    expect(peekPrefetchedNodeState('prefetched-state-node', '7d', 'test-admin-token')).toEqual(expect.objectContaining({ nodeId: 'prefetched-state-node' }))
  })

  it('does not reuse prefetched history across different admin credentials', async () => {
    globalThis.fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ node_id: 'isolated-state-node', range: '7d', points: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ node_id: 'isolated-state-node', range: '7d', points: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })) as unknown as typeof fetch

    await prefetchNodeState('isolated-state-node', '7d', 'first-admin-token')
    await fetchNodeState('isolated-state-node', '7d', 'second-admin-token')

    expect(globalThis.fetch).toHaveBeenCalledTimes(2)
    expect(globalThis.fetch).toHaveBeenNthCalledWith(2, '/api/public/v1/nodes/isolated-state-node/state?range=7d', {
      signal: undefined,
      headers: { Accept: 'application/json', 'X-Admin-Token': 'second-admin-token' },
    })
  })

  it('does not reuse prefetched history after the cookie session rotates', async () => {
    globalThis.fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ node_id: 'rotated-state-node', range: '30d', points: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ node_id: 'rotated-state-node', range: '30d', points: [] }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })) as unknown as typeof fetch

    clearStoredAdminToken()
    rememberAdminToken()
    await prefetchNodeState('rotated-state-node', '30d', loadStoredAdminToken())
    clearStoredAdminToken()
    rememberAdminToken()
    await fetchNodeState('rotated-state-node', '30d', loadStoredAdminToken())

    expect(globalThis.fetch).toHaveBeenCalledTimes(2)
  })
})

describe('normalizeNodeLatency', () => {
  it('keeps node id, range, and loss-only null latency points', () => {
    const data = normalizeNodeLatency({
      node_id: 'example-node-a',
      range: '1h',
      points: [
        { ts: '2026-07-02T12:00:00Z', target_id: 'telegram-dc1', target_name: 'Telegram DC1', median_ms: null, loss_percent: 100 },
        { ts: '2026-07-02T12:02:00Z', target_id: 'google', target_name: 'Google', median_ms: 0.8, loss_percent: 0 },
      ],
    })

    expect(data.nodeId).toBe('example-node-a')
    expect(data.range).toBe('1h')
    expect(data.points[0].targetName).toBe('Telegram DC1')
    expect(data.points[0].medianMs).toBeNull()
    expect(data.points[0].lossPercent).toBe(100)
  })

  it('expands compact Kulin-style target series into chart points', () => {
    const data = normalizeNodeLatency({
      node_id: 'example-node-a',
      range: '1d',
      series: [
        {
          target_id: 'google',
          target_name: 'Google',
          created_at: [Date.parse('2026-07-02T12:00:00Z'), Date.parse('2026-07-02T12:01:00Z')],
          median_ms: [1.2, null],
          avg_ms: [1.4, null],
          loss_percent: [0, 100],
        },
      ],
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', targetId: 'google', targetName: 'Google', medianMs: 1.2, avgMs: 1.4, lossPercent: 0 }),
      expect.objectContaining({ ts: '2026-07-02T12:01:00.000Z', targetId: 'google', targetName: 'Google', medianMs: null, avgMs: null, lossPercent: 100 }),
    ])
  })

  it('expands node latency series with shared timestamps', () => {
    const data = normalizeNodeLatency({
      node_id: 'example-node-a',
      range: '1h',
      created_at: [Date.parse('2026-07-02T12:00:00Z')],
      series: [
        {
          target_id: 'google',
          target_name: 'Google',
          avg_ms: [1.4],
          loss_percent: [0],
        },
        {
          target_id: 'dc1',
          target_name: 'DC1',
          avg_ms: [20],
          loss_percent: [100],
        },
      ],
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', tsMs: Date.parse('2026-07-02T12:00:00Z'), targetId: 'google', targetName: 'Google', avgMs: 1.4, lossPercent: 0 }),
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', tsMs: Date.parse('2026-07-02T12:00:00Z'), targetId: 'dc1', targetName: 'DC1', avgMs: 20, lossPercent: 100 }),
    ])
  })

  it('fingerprints the complete compact chart snapshot for duplicate live-frame suppression', () => {
    const input = {
      node_id: 'example-node-a',
      range: '1d',
      created_at: [Date.parse('2026-07-02T12:00:00Z'), Date.parse('2026-07-02T12:01:00Z')],
      series: [{
        target_id: 'google',
        target_name: 'Google',
        median_ms: [1.3, 1.4],
        avg_ms: [1.4, 1.5],
        loss_percent: [0, 0],
      }],
    }
    const same = structuredClone(input)
    const changed = structuredClone(input)
    changed.series[0].avg_ms[0] = 1.400_000_1
    const changedMedian = structuredClone(input)
    changedMedian.series[0].median_ms[0] = 8.8

    expect(nodeLatencySnapshotKey(same)).toBe(nodeLatencySnapshotKey(input))
    expect(nodeLatencySnapshotKey(changed)).not.toBe(nodeLatencySnapshotKey(input))
    expect(nodeLatencySnapshotKey(changedMedian)).not.toBe(nodeLatencySnapshotKey(input))
    expect(normalizeNodeLatency(input).snapshotKey).toBe(nodeLatencySnapshotKey(input))
  })

  it('fingerprints the same legacy points branch used by normalization', () => {
    const input = {
      node_id: 'legacy-node',
      range: '1h',
      series: [],
      points: [{
        ts: '2026-07-02T12:00:00Z',
        target_id: 'google',
        target_name: 'Google',
        median_ms: 1.2,
        avg_ms: 1.4,
        loss_percent: 0,
      }],
    }
    const changed = structuredClone(input)
    changed.points[0].median_ms = 8.8

    expect(nodeLatencySnapshotKey(changed)).not.toBe(nodeLatencySnapshotKey(input))
  })
})

describe('normalizeNodeState', () => {
  it('maps persisted agent state history into frontend camelCase points', () => {
    const data = normalizeNodeState({
      node_id: 'example-node-a',
      range: '1h',
      points: [
        {
          ts: '2026-07-02T12:00:00Z',
          cpu_percent: 18.75,
          load1: 0.42,
          load5: 0.35,
          load15: 0.28,
          memory_used_bytes: 4096,
          memory_total_bytes: 8192,
          swap_used_bytes: 512,
          swap_total_bytes: 2048,
          disk_used_bytes: 1024,
          disk_total_bytes: 2048,
          net_in_total_bytes: 1000,
          net_out_total_bytes: 2000,
          net_in_speed_bps: 128,
          net_out_speed_bps: 256,
          process_count: 88,
          tcp_connection_count: 34,
          udp_connection_count: 12,
          uptime_seconds: 3601,
        },
      ],
    })

    expect(data.nodeId).toBe('example-node-a')
    expect(data.range).toBe('1h')
    expect(data.points[0].cpuPercent).toBe(18.75)
    expect(data.points[0].load1).toBe(0.42)
    expect(data.points[0].load5).toBe(0.35)
    expect(data.points[0].load15).toBe(0.28)
    expect(data.points[0].memoryUsedBytes).toBe(4096)
    expect(data.points[0].swapUsedBytes).toBe(512)
    expect(data.points[0].swapTotalBytes).toBe(2048)
    expect(data.points[0].netOutSpeedBps).toBe(256)
    expect(data.points[0].processCount).toBe(88)
    expect(data.points[0].tcpConnectionCount).toBe(34)
    expect(data.points[0].udpConnectionCount).toBe(12)
    expect(data.points[0].uptimeSeconds).toBe(3601)
  })

  it('normalizes old state payloads without extra metrics to nulls', () => {
    const data = normalizeNodeState({
      node_id: 'example-node-a',
      range: '1h',
      points: [
        {
          ts: '2026-07-02T12:00:00Z',
          cpu_percent: 18.75,
          memory_used_bytes: 4096,
          memory_total_bytes: 8192,
          disk_used_bytes: 1024,
          disk_total_bytes: 2048,
          net_in_total_bytes: 1000,
          net_out_total_bytes: 2000,
          net_in_speed_bps: 128,
          net_out_speed_bps: 256,
          uptime_seconds: 3601,
        },
      ],
    })

    expect(data.points[0].load1).toBeNull()
    expect(data.points[0].swapUsedBytes).toBeNull()
    expect(data.points[0].processCount).toBeNull()
    expect(data.points[0].tcpConnectionCount).toBeNull()
    expect(data.points[0].udpConnectionCount).toBeNull()
  })

  it('expands compact state series payloads', () => {
    const data = normalizeNodeState({
      node_id: 'example-node-a',
      range: '1d',
      created_at: [Date.parse('2026-07-02T12:00:00Z'), Date.parse('2026-07-02T12:00:30Z')],
      series: {
        cpu_percent: [10, 20],
        load1: [0.1, 0.2],
        memory_used_bytes: [100, 200],
        memory_total_bytes: [1000, 1000],
        disk_used_bytes: [300, 400],
        disk_total_bytes: [2000, 2000],
        net_in_speed_bps: [5, 6],
        net_out_speed_bps: [7, 8],
        process_count: [90, 91],
        uptime_seconds: [3600, 3630],
      },
    })

    expect(data.points).toEqual([
      expect.objectContaining({ ts: '2026-07-02T12:00:00.000Z', cpuPercent: 10, load1: 0.1, memoryUsedBytes: 100, netOutSpeedBps: 7, processCount: 90 }),
      expect.objectContaining({ ts: '2026-07-02T12:00:30.000Z', cpuPercent: 20, load1: 0.2, memoryUsedBytes: 200, netOutSpeedBps: 8, processCount: 91 }),
    ])
  })
})

describe('normalizeSettings', () => {
  it('maps public/admin settings into frontend camelCase models', () => {
    const settings = normalizeSettings({
      site_title: '水饺监控',
      logo_url: '/assets/logo/custom.png',
      theme: 'dark',
      agent_controller_url: 'https://zeno.example.com',
      background_url: 'https://example.com/bg.webp',
      appearance_preset: 'gaussian_blur',
      server_card_theme: 'capsule',
      card_opacity: 0.58,
      card_blur: 18,
      card_radius: 24,
      border_strength: 0.34,
      shadow_strength: 0.34,
      background_overlay: 0.08,
      theme_color: '#6366f1',
      custom_code: '<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>',
      updated_at: '2026-07-04T12:00:00Z',
    })

    expect(settings.siteTitle).toBe('水饺监控')
    expect(settings.logoUrl).toBe('/assets/logo/custom.png')
    expect(settings.theme).toBe('dark')
    expect(settings.agentControllerUrl).toBe('https://zeno.example.com')
    expect(settings.backgroundUrl).toBe('https://example.com/bg.webp')
    expect(settings.appearancePreset).toBe('gaussian_blur')
    expect((settings as unknown as { serverCardTheme: string }).serverCardTheme).toBe('capsule')
    expect(settings.cardOpacity).toBe(0.58)
    expect(settings.cardBlur).toBe(18)
    expect(settings.cardRadius).toBe(24)
    expect(settings.borderStrength).toBe(0.34)
    expect(settings.shadowStrength).toBe(0.34)
    expect(settings.backgroundOverlay).toBe(0.08)
    expect(settings.themeColor).toBe('#6366f1')
    expect(settings.customCode).toBe('<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>')
    expect(settings.updatedAt).toBe('2026-07-04T12:00:00Z')
  })

  it('defaults missing or invalid server card themes to classic', () => {
    const base = {
      site_title: 'Zeno', logo_url: '', theme: 'system' as const, background_url: '',
    }
    expect(normalizeSettings(base).serverCardTheme).toBe('classic')
    expect(normalizeSettings({ ...base, server_card_theme: 'rack' as never }).serverCardTheme).toBe('classic')
  })
})
