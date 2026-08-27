import { afterEach, describe, expect, it, vi } from 'vitest'
import { fetchAdminAccount, fetchAdminSettings, fetchPublicSettings, loginAdmin, logoutAdmin, updateAdminAccount, updateAdminSettings } from './client'
import { probeAdminCookieSession, rememberAdminCookieSessionProbe, resetAdminCookieSessionProbe } from './adminSession'
import { adminCookieSessionMarker } from '../lib/adminToken'

describe('admin auth client', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    resetAdminCookieSessionProbe()
    vi.restoreAllMocks()
  })

  it('coalesces the homepage and dashboard cookie probes and remembers their result', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ account: { username: 'admin' } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await expect(Promise.all([probeAdminCookieSession(), probeAdminCookieSession()])).resolves.toEqual([true, true])
    expect(fetchMock).toHaveBeenCalledTimes(1)

    rememberAdminCookieSessionProbe(false)
    await expect(probeAdminCookieSession()).resolves.toBe(false)
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('uses the HttpOnly cookie marker and CSRF header for browser auth without retaining response tokens', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      const textUrl = String(url)
      if (textUrl.endsWith('/login')) return new Response(JSON.stringify({ username: 'admin' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (textUrl.endsWith('/account') && !init?.method) return new Response(JSON.stringify({ account: { username: 'admin' } }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      if (textUrl.endsWith('/account')) return new Response(JSON.stringify({ username: 'zeno-admin' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
      return new Response(null, { status: 204 })
    })
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await expect(loginAdmin('admin', 'admin-pass')).resolves.toEqual({ username: 'admin', token: adminCookieSessionMarker })
    await expect(fetchAdminAccount(adminCookieSessionMarker)).resolves.toEqual({ username: 'admin' })
    await expect(updateAdminAccount(adminCookieSessionMarker, 'zeno-admin', 'admin-pass', 'new-admin-pass')).resolves.toEqual({ username: 'zeno-admin', token: adminCookieSessionMarker })
    await expect(logoutAdmin(adminCookieSessionMarker)).resolves.toBeUndefined()

    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/login', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Zeno-CSRF': '1' },
      body: JSON.stringify({ username: 'admin', password: 'admin-pass' }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/account', {
      headers: { Accept: 'application/json', 'X-Zeno-CSRF': '1' },
    })
    expect(fetchMock).toHaveBeenNthCalledWith(3, '/api/admin/v1/account', {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json', 'X-Zeno-CSRF': '1' },
      body: JSON.stringify({ username: 'zeno-admin', current_password: 'admin-pass', new_password: 'new-admin-pass' }),
    })
    expect(fetchMock).toHaveBeenNthCalledWith(4, '/api/admin/v1/logout', {
      method: 'POST',
      headers: { 'X-Zeno-CSRF': '1' },
    })
  })

  it('rejects failed logout responses instead of reporting a local logout success', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 500 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await expect(logoutAdmin('account-session-token')).rejects.toThrow('admin logout failed: 500')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/logout', {
      method: 'POST',
      headers: { 'X-Admin-Token': 'account-session-token' },
    })
  })

  it('surfaces logout 401s so the UI can run the expired-session cleanup path', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 401 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await expect(logoutAdmin('expired-session-token')).rejects.toThrow('admin logout failed: 401')
  })
})
describe('fetchSettings', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('fetches public settings without admin credentials', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      site_title: '水饺监控',
      logo_url: '/assets/logo/custom.png',
      theme: 'dark',
      agent_controller_url: 'https://zeno.example.com',
      background_url: 'https://example.com/desktop-bg.webp',
      desktop_background_url: 'https://example.com/desktop-bg.webp',
      mobile_background_url: 'https://example.com/mobile-bg.webp',
      appearance_preset: 'gaussian_blur',
      server_card_theme: 'capsule',
      card_opacity: 0.58,
      card_blur: 18,
      card_radius: 24,
      border_strength: 0.34,
      shadow_strength: 0.34,
      background_overlay: 0.08,
      theme_color: '#6366f1',
      custom_code: '<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>',
      updated_at: '2026-07-04T12:00:00Z',
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const settings = await fetchPublicSettings()

    expect(settings.siteTitle).toBe('水饺监控')
    expect(settings.logoUrl).toBe('/assets/logo/custom.png')
    expect(settings).not.toHaveProperty('avatarUrl')
    expect(settings.desktopBackgroundUrl).toBe('https://example.com/desktop-bg.webp')
    expect(settings.mobileBackgroundUrl).toBe('https://example.com/mobile-bg.webp')
    expect(settings.appearancePreset).toBe('gaussian_blur')
    expect((settings as unknown as { serverCardTheme: string }).serverCardTheme).toBe('capsule')
    expect(settings.cardBlur).toBe(18)
    expect(settings.themeColor).toBe('#6366f1')
    expect(settings.customCode).toBe('<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>')
    expect(fetchMock).toHaveBeenCalledWith('/api/public/v1/settings', {
      headers: { Accept: 'application/json' },
    })
  })

  it('fetches and updates admin settings with X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async (url: string | URL | Request) => new Response(JSON.stringify({
      settings: {
        site_title: String(url).includes('admin') ? '水饺监控' : 'Zeno',
        logo_url: '/assets/logo/custom.png',
        theme: 'dark',
        agent_controller_url: 'https://zeno.example.com',
        background_url: 'https://example.com/desktop-bg.webp',
        desktop_background_url: 'https://example.com/desktop-bg.webp',
        mobile_background_url: 'https://example.com/mobile-bg.webp',
        appearance_preset: 'gaussian_blur',
        server_card_theme: 'capsule',
        card_opacity: 0.58,
        card_blur: 18,
        card_radius: 24,
        border_strength: 0.34,
        shadow_strength: 0.34,
        background_overlay: 0.08,
        theme_color: '#6366f1',
        custom_code: '<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>',
        revision: 4,
        updated_at: '2026-07-04T12:00:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminSettings('admin-pass')
    const settings = await updateAdminSettings('admin-pass', {
      expectedRevision: 4,
      siteTitle: '水饺监控',
      logoUrl: '/assets/logo/custom.png',
      theme: 'dark',
      agentControllerUrl: 'https://zeno.example.com',
      backgroundUrl: 'https://example.com/desktop-bg.webp',
      desktopBackgroundUrl: 'https://example.com/desktop-bg.webp',
      mobileBackgroundUrl: 'https://example.com/mobile-bg.webp',
      appearancePreset: 'gaussian_blur',
      serverCardTheme: 'capsule',
      cardOpacity: 0.58,
      cardBlur: 18,
      cardRadius: 24,
      borderStrength: 0.34,
      shadowStrength: 0.34,
      backgroundOverlay: 0.08,
      themeColor: '#6366f1',
      customCode: '<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>',
    })

    expect(settings.backgroundUrl).toBe('https://example.com/desktop-bg.webp')
    expect(settings.logoUrl).toBe('/assets/logo/custom.png')
    expect(settings.agentControllerUrl).toBe('https://zeno.example.com')
    expect(settings).not.toHaveProperty('avatarUrl')
    expect(settings.desktopBackgroundUrl).toBe('https://example.com/desktop-bg.webp')
    expect(settings.mobileBackgroundUrl).toBe('https://example.com/mobile-bg.webp')
    expect(settings.appearancePreset).toBe('gaussian_blur')
    expect((settings as unknown as { serverCardTheme: string }).serverCardTheme).toBe('capsule')
    expect(settings.cardOpacity).toBe(0.58)
    expect(settings.cardBlur).toBe(18)
    expect(settings.cardRadius).toBe(24)
    expect(settings.borderStrength).toBe(0.34)
    expect(settings.shadowStrength).toBe(0.34)
    expect(settings.backgroundOverlay).toBe(0.08)
    expect(settings.themeColor).toBe('#6366f1')
    expect(settings.customCode).toBe('<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>')
    expect(fetchMock).toHaveBeenNthCalledWith(1, '/api/admin/v1/settings', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
    expect(fetchMock).toHaveBeenNthCalledWith(2, '/api/admin/v1/settings', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        expected_revision: 4,
        site_title: '水饺监控',
        logo_url: '/assets/logo/custom.png',
        theme: 'dark',
        agent_controller_url: 'https://zeno.example.com',
        background_url: 'https://example.com/desktop-bg.webp',
        desktop_background_url: 'https://example.com/desktop-bg.webp',
        mobile_background_url: 'https://example.com/mobile-bg.webp',
        appearance_preset: 'gaussian_blur',
        server_card_theme: 'capsule',
        card_opacity: 0.58,
        card_blur: 18,
        card_radius: 24,
        border_strength: 0.34,
        shadow_strength: 0.34,
        background_overlay: 0.08,
        theme_color: '#6366f1',
        custom_code: '<style>.home-top-card { border-color: #2563eb; }</style><script>window.ZenoCustomLoaded = true;</script>',
      }),
    })
    expect(settings.revision).toBe(4)
  })

  it('returns the latest settings with a typed conflict error', async () => {
    const latest = {
      site_title: 'newer title', logo_url: '', theme: 'system', background_url: '',
      revision: 8,
    }
    const fetchMock = vi.fn(async (_url: string | URL | Request, init?: RequestInit) => (
      init?.method === 'PATCH'
        ? new Response(JSON.stringify({ error: 'settings changed' }), { status: 409, headers: { 'Content-Type': 'application/json' } })
        : new Response(JSON.stringify({ settings: latest }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    ))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const promise = updateAdminSettings('admin-pass', { expectedRevision: 7, siteTitle: 'stale title' })
    await expect(promise).rejects.toMatchObject({
      name: 'AdminSettingsConflictError',
      latestSettings: { siteTitle: 'newer title', revision: 8 },
    })
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })
})
