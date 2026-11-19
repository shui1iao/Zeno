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
  it('splits resources into six small multiples, each on its own unit, all drawn by default', () => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={points} range="1d" canUseExtendedRanges />)

    expect(html).toContain('<h3>系统资源</h3>')
    expect(html).toContain('resource history range selector')
    expect(html).toContain('7 天')
    expect(html).not.toContain('温度')

    // Six independent small charts, one per metric, never a single mixed 0-100% canvas.
    expect(html).toContain('class="resource-multiples"')
    expect(html.match(/data-metric="/g)).toHaveLength(6)
    expect(html.match(/class="latency-chart resource-mini-chart"/g)).toHaveLength(6)
    expect(html).not.toContain('class="latency-chart resource-chart"')
    expect(html).toContain('18.8%')
    const tile = (from: string, to?: string) => html.slice(html.indexOf(`data-metric="${from}"`), to ? html.indexOf(`data-metric="${to}"`) : undefined)
    const cpuTile = tile('cpu', 'memory')
    expect(cpuTile).toContain('<span class="resource-multiple__text">0.42 / 0.35 / 0.28</span>')
    expect(cpuTile).toContain('>100%<')
    expect(cpuTile).not.toContain('负载')
    expect(html).toContain('2.0 KB / 4.0 KB')
    expect(html).toContain('4.0 KB / 8.0 KB')
    const networkTile = tile('network', 'processes')
    expect(networkTile).toContain('<strong class="resource-multiple__value">↓ 64.0 KB/s</strong>')
    expect(networkTile).toContain('↑ 512.0 KB/s')
    // Rates keep a real-unit axis instead of being squeezed onto a percent axis.
    expect(networkTile).toMatch(/>\d+ KB\/s</)
    expect(networkTile).not.toContain('>100%<')
    expect(tile('processes', 'connections')).toContain('>88<')
    expect(tile('connections')).toContain('34 / 12')
    for (const key of ['cpu', 'memory', 'swap', 'disk', 'net-out', 'net-in', 'processes', 'tcp', 'udp']) {
      expect(html).toContain(`data-series="${key}"`)
    }
    expect(html).toMatch(/class="axis-label"[^>]*>\d{2}:\d{2}</)
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

  it('puts the latest Swap usage on the memory tile and leaves it out without swap', () => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={points} range="1d" />)
    const memoryTile = html.slice(html.indexOf('data-metric="memory"'), html.indexOf('data-metric="disk"'))
    expect(memoryTile).toContain('<span class="resource-multiple__text">2.0 KB / 4.0 KB · Swap 0.50 / 2.0 KB</span>')
    expect(memoryTile).toContain('data-series="swap"')
    expect(memoryTile).toMatch(/title="[^"]*Swap 25(\.0)?% · 0\.50 \/ 2\.0 KB"/)

    const noSwap = points.map((point) => ({ ...point, swapUsedBytes: 0, swapTotalBytes: 0 }))
    const noSwapHtml = renderToStaticMarkup(<StateHistoryPanel points={noSwap} range="1d" />)
    const noSwapTile = noSwapHtml.slice(noSwapHtml.indexOf('data-metric="memory"'), noSwapHtml.indexOf('data-metric="disk"'))
    expect(noSwapTile).not.toContain('Swap')
  })

  it.each([
    { used: 802304000, amount: '0.75' },
    { used: 153 * 1024 ** 2, amount: '0.15' },
  ])('groups fractional Swap $amount and its total with unit without changing the displayed data', ({ used, amount }) => {
    const withSwap = points.map((point) => ({ ...point, swapUsedBytes: used, swapTotalBytes: 2147479552 }))
    const html = renderToStaticMarkup(<StateHistoryPanel points={withSwap} range="1d" />)
    const memoryTile = html.slice(html.indexOf('data-metric="memory"'), html.indexOf('data-metric="disk"'))
    const text = memoryTile.match(/<span class="resource-multiple__text">(.*?)<\/span>/)?.[1]
    expect(text).toBe(`2.0 KB / 4.0 KB · Swap ${amount} / 2.0 GB`)
    expect(memoryTile).toContain('<strong class="resource-multiple__value">50.0%</strong>')
  })

  it('opens with the 原始 / 平滑 switch on 平滑', () => {
    const html = renderToStaticMarkup(<StateHistoryPanel points={points} range="1d" />)
    const switchHtml = html.slice(html.indexOf('aria-label="资源曲线平滑"'), html.indexOf('resource history range selector'))
    expect(switchHtml).toContain('原始')
    expect(switchHtml).toMatch(/aria-(pressed|checked|selected)="true"[^>]*>(<[^>]+>)*平滑/)
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
