import { afterEach, describe, expect, it, vi } from 'vitest'
import { loginAdmin, probeAdminCookieSession, resetAdminCookieSessionProbe, updateAdminAccount } from './adminSession'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('admin password requests', () => {
  it('preserves surrounding password whitespace for login and account updates', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ username: 'admin' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ username: 'admin', token: 'rotated' }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

    const rawPassword = ' new-admin-pass '
    await loginAdmin('admin', rawPassword)
    await updateAdminAccount('session-token', 'admin', rawPassword, rawPassword)

    const loginBody = JSON.parse(String(fetchMock.mock.calls[0]?.[1]?.body)) as { password: string }
    const updateBody = JSON.parse(String(fetchMock.mock.calls[1]?.[1]?.body)) as { current_password: string; new_password: string }
    expect(loginBody.password).toBe(rawPassword)
    expect(updateBody.current_password).toBe(rawPassword)
    expect(updateBody.new_password).toBe(rawPassword)
  })
})

describe('admin cookie session probe', () => {
  afterEach(() => resetAdminCookieSessionProbe())

  it('uses the status endpoint and treats an anonymous 200 as signed out', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: false }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await expect(probeAdminCookieSession()).resolves.toBe(false)
    expect(String(fetchMock.mock.calls[0]?.[0])).toBe('/api/admin/v1/session')
    const headers = fetchMock.mock.calls[0]?.[1]?.headers as Record<string, string>
    expect(headers['X-Admin-Token']).toBeUndefined()
  })

  it('recognises an authenticated cookie session', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify({ authenticated: true }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await expect(probeAdminCookieSession()).resolves.toBe(true)
  })

  it('falls back to signed out when an older controller lacks the endpoint', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(new Response('not found', { status: 404 }))
    await expect(probeAdminCookieSession()).resolves.toBe(false)
  })
})
