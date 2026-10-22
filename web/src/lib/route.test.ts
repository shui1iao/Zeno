import { describe, expect, it } from 'vitest'
import { parseDashboardRoute } from './route'

describe('parseDashboardRoute', () => {
  it('keeps the public home page on slash-like paths', () => {
    expect(parseDashboardRoute('/')).toEqual({ kind: 'home' })
    expect(parseDashboardRoute('/index.html')).toEqual({ kind: 'home' })
  })

  it('routes the login/backend entry to the admin dashboard shell', () => {
    expect(parseDashboardRoute('/dashboard')).toEqual({ kind: 'admin' })
    expect(parseDashboardRoute('/dashboard/')).toEqual({ kind: 'admin' })
  })

  it('extracts decoded node ids from Kulin-style server detail URLs', () => {
    expect(parseDashboardRoute('/server/example-harbor')).toEqual({ kind: 'node', nodeId: 'example-harbor' })
    expect(parseDashboardRoute('/server/Example%20Edge%20HK')).toEqual({ kind: 'node', nodeId: 'Example Edge HK' })
  })

  it('treats the removed service pages like any other unknown path', () => {
    expect(parseDashboardRoute('/services')).toEqual({ kind: 'home' })
    expect(parseDashboardRoute('/services/')).toEqual({ kind: 'home' })
    expect(parseDashboardRoute('/service/google')).toEqual({ kind: 'home' })
    expect(parseDashboardRoute('/service/Akari%20HK')).toEqual({ kind: 'home' })
  })

  it('falls back to the safe canonical route for malformed percent-encoded ids', () => {
    expect(() => parseDashboardRoute('/server/%')).not.toThrow()
    expect(parseDashboardRoute('/server/%E0%A4%A')).toEqual({ kind: 'home' })
    expect(parseDashboardRoute('/service/%FF')).toEqual({ kind: 'home' })
  })
})
