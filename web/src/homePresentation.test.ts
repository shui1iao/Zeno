// @ts-expect-error Node APIs are available in Vitest, not the browser tsconfig.
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import postcss from 'postcss'

const root = postcss.parse(readFileSync(new URL('./styles.css', import.meta.url), 'utf8'))
function declarations(selector: string, media?: string) {
  const values: Record<string, string> = {}
  root.walkRules(selector, rule => {
    const parent = rule.parent
    const condition = parent?.type === 'atrule' ? parent.params : undefined
    if (condition !== media) return
    rule.walkDecls(decl => { values[decl.prop] = decl.value })
  })
  return values
}

describe('homepage icon-led presentation', () => {
  it('keeps the original centered card header and server reading sizes', () => {
    expect(declarations('.node-title-line p')['font-size']).toBe('13px')
    expect(declarations('.node-title-line p')['font-weight']).toBe('600')
    expect(declarations('.node-title-line')['justify-content']).toBe('center')
    for (const selector of ['.node-uptime', '.node-specs', '.node-metric', '.node-health-metric', '.node-metric strong', '.health-history-heading strong']) {
      expect(declarations(selector)['font-size'], selector).toBe('11px')
    }
    expect(declarations('.usage-row__meta')['font-size']).toBe('12px')
  })

  it('stacks overview label over a large tabular reading on desktop and keeps compact inline rows on phones', () => {
    expect(declarations('.home-summary__metric')['min-height']).toBe('40px')
    expect(declarations('.home-summary__metric')['flex-direction']).toBe('column')
    expect(declarations('.home-summary__metric')['font-variant-numeric']).toBe('tabular-nums')
    expect(declarations('.home-summary__metric-label')['font-size']).toBe('11px')
    expect(declarations('.home-summary__metric-value strong')['font-size']).toBe('19px')
    expect(declarations('.home-summary__metric-value strong')['font-weight']).toBe('500')
    expect(declarations('.home-summary__metric-value > span')['font-size']).toBe('11px')
    expect(declarations('.home-summary__metric', '(max-width: 767px)')['min-height']).toBe('40px')
    expect(declarations('.home-summary__metric', '(max-width: 767px)')['flex-direction']).toBe('row')
    expect(declarations('.home-summary__metric-value strong', '(max-width: 767px)')['font-size']).toBe('13.5px')
    expect(declarations('.home-summary__metric-value strong', '(max-width: 360px)')['font-size']).toBe('12.5px')
    expect(declarations('.home-summary__metric-value > span', '(max-width: 360px)')['font-size']).toBe('9.5px')
    expect(declarations('.home-top-card .home-summary')['grid-template-columns']).toBe('repeat(6, minmax(0, 1fr))')
    expect(declarations('.home-top-card .home-summary', '(min-width: 768px) and (max-width: 1023px)')['grid-template-columns']).toBe('repeat(3, minmax(0, 1fr))')
    expect(declarations('.home-top-card .home-summary', '(max-width: 767px)')['grid-template-columns']).toBe('repeat(2, minmax(0, 1fr))')
  })

  it('uses theme-neutral readings, muted units, and keeps color on the icons', () => {
    for (const selector of ['.home-summary__metric-label', '.home-summary__metric-value', '.home-summary__metric-value strong', '.metric-label', '.node-metric strong', '.health-history-heading strong', '.usage-row__meta span', '.usage-row__meta strong', '.kulin-node-card.is-capsule .usage-row__meta strong', '.kulin-node-card.is-capsule .usage-row__detail']) {
      expect(declarations(selector).color, selector).toBe('var(--foreground)')
    }
    expect(declarations('.home-summary__metric-value > span').color).toBe('var(--muted)')
    expect(declarations('.home-summary__metric-icon').color).toBe('var(--summary-accent, var(--orange))')
    expect(declarations('.health-history-heading').color).toBe('var(--metric-accent)')
  })

  it('keeps overview icons 16px and restores original 18px card metric icons and 16px spec icons', () => {
    for (const selector of ['.home-summary__metric-icon', '.home-summary__metric-icon svg', '.home-summary__status-dot']) {
      expect(declarations(selector).width, selector).toBe('16px')
      expect(declarations(selector).height, selector).toBe('16px')
    }
    for (const selector of ['.metric-icon', '.metric-icon svg']) {
      expect(declarations(selector).width, selector).toBe('18px')
      expect(declarations(selector).height, selector).toBe('18px')
    }
    expect(declarations('.node-spec svg').width).toBe('16px')
    expect(declarations('.node-spec svg').height).toBe('16px')
  })

  it('adds a little icon-to-text space on cards and the overview', () => {
    expect(declarations('.node-spec').gap).toBe('6px')
    expect(declarations('.metric-heading').gap).toBe('6px')
    expect(declarations('.home-summary__metric-label').gap).toBe('7px')
    expect(declarations('.home-summary__metric-label', '(max-width: 767px)').gap).toBe('6px')
    expect(declarations('.home-summary__metric-label', '(max-width: 360px)').gap).toBe('5px')
  })

  it('preserves light server-only shadows and green-online/red-offline status on the redesigned canvas', () => {
    expect(declarations('.kulin-node-card')['--zeno-node-shadow']).toBe('0 4px 14px -4px rgba(0, 0, 0, .06)')
    expect(declarations(':root')['--zeno-node-shadow']).toBeUndefined()
    expect(declarations('.home-top-card')['--zeno-node-shadow']).toBeUndefined()
    expect(declarations('.kulin-node-card.is-offline .node-uptime').color).toBe('var(--metric-red)')
    expect(declarations('.node-uptime').color).toBe('var(--metric-green)')
    expect(declarations(':root')['--background']).toBe('light-dark(#f4f6fa, #07111f)')
    expect(declarations(':root')['--page-surface']).toBe('light-dark(#ffffff, #0f172a)')
  })
})
