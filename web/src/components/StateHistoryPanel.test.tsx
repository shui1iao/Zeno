import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import type { StatePoint } from '../types'
import { buildChartView, formatResourceUsage, StateHistoryPanel, type ResourceMetric } from './StateHistoryPanel'

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
  it('shows six metric tiles and, like the latency chart, draws every metric by default', () => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={points} range="1d" canUseExtendedRanges />)

    expect(html).toContain('系统资源趋势')
    expect(html).toContain('resource history range selector')
    expect(html).toContain('7 天')
    expect(html).not.toContain('温度')

    // Tiles reuse the latency target grid; nothing is selected by default.
    expect(html).toContain('latency-target-grid resource-metric-grid')
    expect(html.match(/data-metric="/g)).toHaveLength(6)
    const tiles = html.slice(html.indexOf('resource-metric-grid'), html.indexOf('resource-chart-panel'))
    expect(tiles).not.toContain('aria-pressed="true"')
    expect(html).toContain('18.8%')
    expect(html).toContain('负载 0.42 / 0.35 / 0.28')
    expect(html).toContain('2.0 KB / 4.0 KB')
    expect(html).toContain('4.0 KB / 8.0 KB')
    expect(html).toContain('↑ 512.0 KB/s')
    expect(html).toContain('↓ 64.0 KB/s')
    expect(html).toContain('>88<')
    expect(html).toContain('34 / 12')
    // Two-line tiles carry no empty third line, so their value can centre.
    const processTile = tiles.slice(tiles.indexOf('data-metric="processes"'), tiles.indexOf('data-metric="connections"'))
    expect(processTile).not.toContain('<em')

    // One big chart in the latency chart's canvas with all nine lines on a 0-100 axis.
    expect(html.match(/class="latency-chart resource-chart"/g)).toHaveLength(1)
    expect(html).toContain('viewBox="0 0 960 360"')
    for (const key of ['cpu', 'memory', 'swap', 'disk', 'net-out', 'net-in', 'processes', 'tcp', 'udp']) {
      expect(html).toContain(`data-series="${key}"`)
    }
    expect(html).toContain('>100%<')
    expect(html).toContain('>75%<')
    expect(html).toMatch(/class="axis-label"[^>]*>\d{2}:\d{2}</)
    expect(html).not.toContain('-area"')
  })

  it('keeps real units for a single-unit selection and rescales rates and counts in a mixed one', () => {
    const series = (key: string, values: Array<number | null>) => ({ key, label: key, color: '#000', values, describe: () => '' })
    const metrics: ResourceMetric[] = [
      { key: 'cpu', title: 'CPU', scale: 'percent', series: [series('cpu', [10, 20])], value: '', detail: '' },
      { key: 'network', title: '网络', scale: 'rate', series: [series('net-out', [512, 1024]), series('net-in', [256, null])], value: '', detail: '' },
    ]
    const networkOnly = buildChartView(metrics, ['network'])
    expect(networkOnly.scale).toBe('rate')
    expect(networkOnly.series.every((item) => item.plot === undefined)).toBe(true)

    const all = buildChartView(metrics, [])
    expect(all.scale).toBe('percent')
    expect(all.series.find((item) => item.key === 'cpu')?.plot).toBeUndefined()
    // Upload and download share the network peak (1024).
    expect(all.series.find((item) => item.key === 'net-out')?.plot).toEqual([50, 100])
    expect(all.series.find((item) => item.key === 'net-in')?.plot).toEqual([25, null])
  })

  it('breaks the line across an offline gap instead of bridging it', () => {
    const base = Date.parse('2026-07-02T12:00:00Z')
    const gapped = [0, 1, 2, 3, 30, 31, 32].map((minute) => ({ ...points[0], ts: new Date(base + minute * 60_000).toISOString() }))
    const html = renderToStaticMarkup(<StateHistoryPanel points={gapped} range="1d" />)
    const cpuGroup = html.slice(html.indexOf('data-series="cpu"'), html.indexOf('data-series="memory"'))
    expect(cpuGroup.match(/class="resource-chart-line"/g)).toHaveLength(2)
  })

  it.each([
    { loading: true, error: undefined, state: 'loading', role: 'status' },
    { loading: false, error: undefined, state: 'empty', role: 'status' },
    { loading: false, error: '请求超时', state: 'error', role: 'alert' },
  ])('announces the $state history state distinctly', ({ loading, error, state, role }) => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={[]} range="1d" loading={loading} error={error} />)
    expect(html).toContain(`data-state="${state}"`)
    expect(html).toContain(`role="${role}"`)
  })

  it('shows an explicit empty state instead of a blank chart', () => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={[]} range="1d" />)
    expect(html).toContain('暂无系统资源趋势数据')
  })

  it('formats concrete resource usage with its percentage', () => {
    expect(formatResourceUsage(2048, 4096)).toBe('2.0 KB / 4.0 KB · 50.0%')
    expect(formatResourceUsage(null, 4096)).toBe('-- / 4.0 KB')
    expect(formatResourceUsage(null, null)).toBe('--')
  })
})
