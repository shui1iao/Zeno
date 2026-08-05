// @ts-nocheck
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { describe, expect, it } from 'vitest'

const sourceDirectory = dirname(fileURLToPath(import.meta.url))
const adminShellStyles = readFileSync(join(sourceDirectory, 'styles/admin-shell.css'), 'utf8')

describe('admin login surface and top-card alignment', () => {
  it('keeps login inputs transparent in every browser interaction state', () => {
    expect(adminShellStyles).toMatch(/\.admin-login-card input\s*\{[^}]*background-color: transparent;[^}]*background-image: none;[^}]*box-shadow: none;[^}]*backdrop-filter: none;[^}]*\}/)
    expect(adminShellStyles).toMatch(/\.admin-login-card input:hover,[\s\S]*?\.admin-login-card input:active\s*\{[^}]*background-color: transparent;[^}]*background-image: none;[^}]*box-shadow: none;[^}]*\}/)
    expect(adminShellStyles).toMatch(/\.admin-login-card input:-webkit-autofill,[\s\S]*?\.admin-login-card input:-webkit-autofill:active\s*\{[^}]*background-color: transparent !important;[^}]*background-image: none !important;[^}]*box-shadow: none !important;[^}]*\}/)
  })

  it('uses one exact front-and-admin top-card height at desktop and phone widths', () => {
    expect(adminShellStyles).toMatch(/\.home-overview-card,[\s\S]*?\.admin-chrome-card\s*\{\s*height: 135px;\s*\}/)
    expect(adminShellStyles).toMatch(/@media \(max-width: 767px\)\s*\{[\s\S]*?\.home-overview-card,[\s\S]*?\.admin-chrome-card\s*\{\s*height: 180px;\s*\}/)
  })

  it('keeps the phone admin navigation on one five-column row', () => {
    expect(adminShellStyles).toMatch(/\.admin-chrome-card \.admin-section-nav\s*\{[^}]*width: 100%;[^}]*grid-template-columns: repeat\(var\(--slider-columns\), minmax\(0, 1fr\)\);[^}]*\}/)
    expect(adminShellStyles).toMatch(/@media \(max-width: 767px\)\s*\{[\s\S]*?\.admin-chrome-card \.admin-section-nav button\s*\{[^}]*height: 24px;[^}]*padding: 0 2px;[^}]*\}/)
    expect(adminShellStyles).not.toContain('.admin-chrome-card .admin-section-nav button:last-child')
  })
})
