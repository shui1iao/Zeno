import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { StatePoint } from '../types'
import { formatResourceUsage, StateHistoryPanel, stateYDomain } from './StateHistoryPanel'

const points: StatePoint[] = [
  {
    ts: '2026-07-02T12:00:00Z',
    cpuPercent: 12.5,
    load1: 0.22,
    load5: 0.18,
    load15: 0.12,
    memoryUsedBytes: 1024,
    memoryTotalBytes: 4096,
    swapUsedBytes: 256,
    swapTotalBytes: 2048,
    diskUsedBytes: 2048,
    diskTotalBytes: 8192,
    netInTotalBytes: 10 * 1024,
    netOutTotalBytes: 20 * 1024,
    netInSpeedBps: 128 * 1024,
    netOutSpeedBps: 256 * 1024,
    processCount: 80,
    tcpConnectionCount: 28,
    udpConnectionCount: 10,
    uptimeSeconds: 3600,
  },
  {
    ts: '2026-07-02T12:01:00Z',
    cpuPercent: 18.75,
    load1: 0.42,
    load5: 0.35,
    load15: 0.28,
    memoryUsedBytes: 2048,
    memoryTotalBytes: 4096,
    swapUsedBytes: 512,
    swapTotalBytes: 2048,
    diskUsedBytes: 4096,
    diskTotalBytes: 8192,
    netInTotalBytes: 12 * 1024,
    netOutTotalBytes: 24 * 1024,
    netInSpeedBps: 64 * 1024,
    netOutSpeedBps: 512 * 1024,
    processCount: 88,
    tcpConnectionCount: 34,
    udpConnectionCount: 12,
    uptimeSeconds: 3660,
  },
]

describe('StateHistoryPanel', () => {
  it('renders real agent state history metrics and sparklines', () => {
    const html = renderToStaticMarkup(
      <StateHistoryPanel points={points} range="1h" loading={false} canUseExtendedRanges />,
    )

    expect(html).toContain('系统资源趋势')
    expect(html).not.toContain('实时 · 2 个状态采样')
    expect(html).not.toContain('个状态采样')
    expect(html).toContain('resource history range selector')
    expect(html).toContain('实时')
    expect(html).toContain('1 天')
    expect(html).toContain('7 天')
    expect(html).toContain('30 天')
    expect(html).not.toContain('运行 1 小时 1 分钟')
    expect(html).not.toContain('温度')
    expect(html).toContain('resource-history-grid')
    expect(html).toContain('resource-card')
    expect(html).toContain('resource-y-axis')
    expect(html).toContain('resource-chart-plot')
    expect(html).toContain('viewBox="0 0 640 112"')
    expect(html).toContain('class="resource-grid-line"')
    expect(html).toContain('class="resource-x-axis"')
    expect(html).not.toContain('>1m</span>')
    expect(html).not.toContain('>0s</span>')
    expect(html).toMatch(/class="resource-x-axis"[^>]*>.*\d{2}:\d{2}/)
    expect(html).toContain('data-series="cpu-area"')
    expect(html).toContain('data-series="memory-area"')
    expect(html).toContain('data-series="processes-area"')
    expect(html).not.toContain('data-series="net-out-area"')
    expect(html).toContain('CPU')
    expect(html).toContain('18.8%')
    expect(html).toContain('内存 / Swap')
    expect(html).toContain('内存 2.0 KB / 4.0 KB · 50.0%')
    expect(html).toContain('Swap 512 B / 2.0 KB · 25.0%')
    expect(html).toContain('磁盘')
    expect(html).toContain('4.0 KB / 8.0 KB · 50.0%')
    expect(html).toContain('网络速率')
    expect(html).toContain('↑512.0 KB/s')
    expect(html).toContain('↓64.0 KB/s')
    expect(html).toContain('进程数')
    expect(html).toContain('TCP / UDP')
    expect(html).toContain('UDP 12')
    expect(html).not.toContain('系统负载')
    expect(html).not.toContain('网络累计')
    expect(html).toContain('data-series="cpu"')
    expect(html).toContain('data-series="memory"')
    expect(html).toContain('data-series="disk"')
    expect(html).toContain('data-series="net-out"')
    expect(html).toContain('data-series="net-in"')
    expect(html).toContain('data-series="swap"')
    expect(html).not.toContain('data-series="load1"')
    expect(html).toContain('data-series="processes"')
    expect(html).toContain('data-series="tcp"')
    expect(html).toContain('data-series="udp"')
    expect(html).not.toContain('data-series="net-out-total"')
  })

  it.each([
    { loading: true, error: undefined, state: 'loading', role: 'status' },
    { loading: false, error: undefined, state: 'empty', role: 'status' },
    { loading: false, error: '请求超时', state: 'error', role: 'alert' },
  ])('announces the $state history state distinctly', ({ loading, error, state, role }) => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={[]} range="1h" loading={loading} error={error} />)
    expect(html).toContain(`data-state="${state}"`)
    expect(html).toContain(`role="${role}"`)
  })

  it('shows an explicit empty state instead of a blank chart', () => {
    const html = renderToStaticMarkup(
      <StateHistoryPanel points={[]} range="1d" loading={false} />,
    )

    expect(html).toContain('暂无系统资源趋势数据')
  })

  it('reduces an exceptionally large state payload without spreading it into Math.max', () => {
    const values = Array.from({ length: 250_000 }, (_, index) => index === 249_999 ? 200 : index % 100)

    expect(() => stateYDomain(values)).not.toThrow()
    expect(stateYDomain(values)).toEqual({ min: 0, max: 230 })
  })

  it('formats concrete resource usage with its percentage for chart values and tooltips', () => {
    expect(formatResourceUsage(2048, 4096)).toBe('2.0 KB / 4.0 KB · 50.0%')
    expect(formatResourceUsage(null, 4096)).toBe('-- / 4.0 KB')
    expect(formatResourceUsage(null, null)).toBe('--')
  })
})
