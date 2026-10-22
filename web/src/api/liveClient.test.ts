import { afterEach, describe, expect, it, vi } from 'vitest'
import { subscribeNodeLatency, subscribeNodeState, subscribeSummary } from './client'

describe('subscribeSummary', () => {
  const originalWebSocket = globalThis.WebSocket
  afterEach(() => {
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: originalWebSocket })
    vi.restoreAllMocks()
  })

  it('prefers websocket summary events and closes the socket', () => {
    const instances: any[] = []
    class FakeWebSocket {
      url: string
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      onerror: (() => void) | null = null
      onclose: (() => void) | null = null
      close = vi.fn(() => this.onclose?.())

      constructor(url: string) {
        this.url = url
        instances.push(this)
      }

      emitSummary(payload: unknown) {
        this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent<string>)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onSummary = vi.fn()
    const onError = vi.fn()
    const unsubscribe = subscribeSummary(onSummary, onError)

    expect(new URL(instances[0].url).pathname).toBe('/api/public/v1/summary/ws')
    expect(new URL(instances[0].url).protocol).toBe('ws:')
    instances[0].emitSummary({
      nodes: [{ id: 'example-node-a', display_name: 'Example Node A', status: 'online', os: 'debian', country_code: 'HK', cpu_percent: 1, memory_used_bytes: 2, memory_total_bytes: 4, disk_used_bytes: 8, disk_total_bytes: 16, net_in_speed_bps: 32, net_out_speed_bps: 64, net_in_total_bytes: 128, net_out_total_bytes: 256, monthly_billable_bytes: 384, monthly_quota_bytes: 512 }],
      services: [],
      latency_points: [],
    })

    expect(onSummary).toHaveBeenCalledWith(expect.objectContaining({
      nodes: [expect.objectContaining({ id: 'example-node-a', displayName: 'Example Node A', netOutSpeedBps: 64 })],
    }))
    unsubscribe?.()
    expect(instances[0].close).toHaveBeenCalled()
    expect(onError).not.toHaveBeenCalled()
  })

  it('reconnects websocket closes without surfacing a fatal API error', () => {
    vi.useFakeTimers()
    const instances: any[] = []
    class FakeWebSocket {
      url: string
      onopen: (() => void) | null = null
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      onerror: (() => void) | null = null
      onclose: (() => void) | null = null
      close = vi.fn(() => this.onclose?.())

      constructor(url: string) {
        this.url = url
        instances.push(this)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onSummary = vi.fn()
    const onError = vi.fn()
    const unsubscribe = subscribeSummary(onSummary, onError)
    instances[0].onopen?.()
    instances[0].onclose?.()
    expect(onError).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1250)
    expect(instances).toHaveLength(2)

    unsubscribe?.()
    vi.useRealTimers()
  })

  it('keeps reconnecting beyond the old 30 attempt ceiling', async () => {
    vi.useFakeTimers()
    const instances: any[] = []
    class FakeWebSocket {
      onopen: (() => void) | null = null
      onclose: (() => void) | null = null
      close = vi.fn(() => this.onclose?.())

      constructor() {
        instances.push(this)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onError = vi.fn()
    const unsubscribe = subscribeSummary(vi.fn(), onError)

    for (let i = 0; i < 35; i += 1) {
      instances[instances.length - 1].onopen?.()
      instances[instances.length - 1].onclose?.()
      await vi.advanceTimersByTimeAsync(30_000)
    }

    expect(instances.length).toBeGreaterThan(30)
    expect(onError).not.toHaveBeenCalled()

    unsubscribe?.()
    vi.useRealTimers()
  })

})

describe('detail websocket subscriptions', () => {
  const originalWebSocket = globalThis.WebSocket

  afterEach(() => {
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: originalWebSocket })
    vi.restoreAllMocks()
  })

  it('opens node state and node latency websocket endpoints', () => {
    const instances: any[] = []
    class FakeWebSocket {
      url: string
      onmessage: ((event: MessageEvent<string>) => void) | null = null
      close = vi.fn()

      constructor(url: string) {
        this.url = url
        instances.push(this)
      }

      emit(payload: unknown) {
        this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent<string>)
      }
    }
    Object.defineProperty(globalThis, 'WebSocket', { configurable: true, writable: true, value: FakeWebSocket })

    const onNodeLatency = vi.fn()
    const onNodeState = vi.fn()
    const stopNodeLatency = subscribeNodeLatency('example-node-a', '1d', onNodeLatency)
    const stopNodeState = subscribeNodeState('example-node-a', '1h', onNodeState)

    expect(new URL(instances[0].url).pathname).toBe('/api/public/v1/nodes/example-node-a/latency/ws')
    expect(new URL(instances[0].url).searchParams.get('range')).toBe('1d')
    expect(new URL(instances[1].url).pathname).toBe('/api/public/v1/nodes/example-node-a/state/ws')
    expect(new URL(instances[1].url).searchParams.get('range')).toBe('1h')

    instances[0].emit({ node_id: 'example-node-a', range: '1d', points: [{ ts: '2026-07-05T07:00:00Z', target_id: 'google', target_name: 'Google', median_ms: 1.2, loss_percent: 0 }] })
    instances[1].emit({ node_id: 'example-node-a', range: '1h', points: [{ ts: '2026-07-05T07:00:00Z', cpu_percent: 12, load1: 0.1, load5: 0.2, load15: 0.3, memory_used_bytes: 100, memory_total_bytes: 200, swap_used_bytes: 0, swap_total_bytes: 0, disk_used_bytes: 300, disk_total_bytes: 400, net_in_total_bytes: 500, net_out_total_bytes: 600, net_in_speed_bps: 700, net_out_speed_bps: 800, process_count: 90, tcp_connection_count: 10, udp_connection_count: 5, uptime_seconds: 60 }] })

    expect(onNodeLatency).toHaveBeenCalledWith(expect.objectContaining({ nodeId: 'example-node-a', range: '1d' }))
    expect(onNodeState).toHaveBeenCalledWith(expect.objectContaining({ nodeId: 'example-node-a', range: '1h', points: [expect.objectContaining({ netOutSpeedBps: 800 })] }))

    stopNodeLatency?.()
    stopNodeState?.()
    expect(instances).toHaveLength(2)
    expect(instances.every((instance) => instance.close.mock.calls.length === 1)).toBe(true)
  })
})
