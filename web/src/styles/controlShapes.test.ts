import { expect, it } from 'vitest'
// @ts-ignore -- Node-side CSS regression test, no app Node typings.
import { readFileSync } from 'node:fs'
import postcss from 'postcss'

it('preserves original navigation and detail selector geometry on touch screens', () => {
  const css = postcss.parse(readFileSync(new URL('./touch.css', import.meta.url), 'utf8'))
  const paintedOverrides: string[] = []
  css.walkRules(rule => {
    if (rule.selector.includes('::') || rule.selector.includes('svg')) return
    if (!/\.nav-icon-button|\.detail-range-row|\.smoothing-selector/.test(rule.selector)) return
    rule.walkDecls(decl => {
      if (/^(width|min-width|height|min-height|grid-template-columns|--selector-height)$/.test(decl.prop)) {
        paintedOverrides.push(`${rule.selector}: ${decl.prop}=${decl.value}`)
      }
    })
  })
  expect(paintedOverrides).toEqual([])
})
