import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { slidingSelectorStyle } from '../lib/slidingSelector'
import { HistoryRangeSelector } from './HistoryRangeSelector'

const options = [
  { value: '1h', label: '实时' },
  { value: '1d', label: '1 天' },
  { value: '7d', label: '7 天' },
]

describe('sliding history range selector', () => {
  it('clamps selector geometry to valid columns and indices', () => {
    expect(slidingSelectorStyle(3, 2)).toEqual({
      '--slider-columns': 3,
      '--slider-width': 'calc(100% / 3)',
      '--slider-shift': '200%',
    })
    expect(slidingSelectorStyle(0, 8)).toEqual({
      '--slider-columns': 1,
      '--slider-width': 'calc(100% / 1)',
      '--slider-shift': '0%',
    })
  })

  it('renders one pressed option and falls back to the first option for stale values', () => {
    const html = renderToStaticMarkup(
      <HistoryRangeSelector ariaLabel="history range" options={options} value="missing" onChange={() => {}} />,
    )

    expect(html).toContain('role="group"')
    expect(html).toContain('aria-label="history range"')
    expect(html.match(/aria-pressed="true"/g)).toHaveLength(1)
    expect(html).toContain('class="is-active" aria-pressed="true">实时</button>')
    expect(html).toContain('--slider-columns:3')
    expect(html).toContain('--slider-shift:0%')
  })
})
