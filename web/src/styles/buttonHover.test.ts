// @ts-nocheck -- CSS contract runs in the Node test runner.
import { readFileSync } from 'node:fs'
import postcss from 'postcss'
import { describe, expect, it } from 'vitest'

const source = readFileSync(new URL('./button-hover.css', import.meta.url), 'utf8')
const css = postcss.parse(source)
const entry = readFileSync(new URL('../main.tsx', import.meta.url), 'utf8')
const base = readFileSync(new URL('../styles.css', import.meta.url), 'utf8')
const hover = css.nodes.find(node => node.type === 'atrule')

describe('bordered button hover consistency', () => {
  it('uses the existing currency-strength theme mix, without changing theme variables', () => {
    const tokens: string[] = []
    css.walkDecls(/^--/, declaration => tokens.push(`${declaration.prop}: ${declaration.value}`))
    expect(tokens).toEqual(['--control-hover-border: color-mix(in srgb, var(--blue) 48%, var(--border))'])
    expect(entry).toContain("import './styles/button-hover.css'")
  })
  it('only paints a border for real fine-pointer hover, never backgrounds, text or geometry', () => {
    expect(hover.params).toBe('(hover: hover) and (pointer: fine)')
    const declarations: string[] = []
    hover.walkDecls(declaration => declarations.push(`${declaration.prop}: ${declaration.value}`))
    expect(declarations).toEqual(['border-color: var(--control-hover-border)'])
    const unsupported: string[] = []
    css.walkDecls(declaration => {
      if (!['--control-hover-border', 'border-color', 'transition'].includes(declaration.prop) || declaration.important) unsupported.push(declaration.toString())
    })
    expect(unsupported).toEqual([])
  })
  it('covers explicit existing control families while excluding semantic and borderless controls', () => {
    const rule = hover.nodes.find(node => node.type === 'rule')
    for (const selector of ['.home-currency-select', '.nav-icon-button', '.login-link', '.region-filter-buttons > button', '.admin-select-popover button', '.admin-date-grid button', '.admin-modal-close', '.admin-row-action', '.admin-action-footer button']) expect(rule.selector).toContain(selector)
    expect(rule.selector).toContain(':hover:not(:disabled):where(')
    for (const excluded of ['[aria-disabled=\'true\']', '.admin-primary-action', '[type=\'submit\']', '.is-danger', '.admin-account-logout-button', '.is-active', '.is-selected', '[data-active=\'true\']', '[aria-pressed=\'true\']', '[aria-expanded=\'true\']', ':focus-visible', '.sliding-selector > button', '.admin-drag-handle', '.admin-assignment-field__header']) expect(rule.selector).toContain(excluded)
    expect(rule.selector).not.toContain('.latency-target-grid')
    expect(rule.selector).not.toContain('.detail-title-button')
    expect(rule.selector).not.toContain('.brand')
  })
  it('adds border transitions without replacing existing navigation transitions', () => {
    for (const name of ['login-link', 'nav-icon-button']) expect(base).toMatch(new RegExp(`\\.${name} \\{[^}]*transition: background-color 150ms ease, border-color 150ms ease, color 150ms ease, box-shadow 150ms ease, transform 150ms ease;`))
    const transition = css.nodes.filter(node => node.type === 'rule').at(-1)
    expect(transition.nodes.map(declaration => `${declaration.prop}: ${declaration.value}`)).toEqual(['transition: border-color 150ms ease'])
  })
})
