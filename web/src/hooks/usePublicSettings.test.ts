import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

type HookRecord = {
  states: unknown[]
  refs: Array<{ current: unknown }>
  effects: Array<{ deps?: unknown[]; cb: () => void | (() => void); cleanup?: () => void }>
  pendingEffects: number[]
}

let activeRecord: HookRecord | null = null
let stateIndex = 0
let refIndex = 0
let effectIndex = 0

function depsChanged(prev: unknown[] | undefined, next: unknown[] | undefined): boolean {
  if (!prev || !next || prev.length !== next.length) return true
  return next.some((value, index) => !Object.is(value, prev[index]))
}

vi.mock('react', async () => {
  const actual = await vi.importActual<typeof import('react')>('react')
  return {
    ...actual,
    useState: (initial: unknown) => {
      const record = activeRecord!
      const idx = stateIndex++
      if (!(idx in record.states)) {
        record.states[idx] = typeof initial === 'function' ? (initial as () => unknown)() : initial
      }
      const setState = (next: unknown) => {
        record.states[idx] = typeof next === 'function'
          ? (next as (prev: unknown) => unknown)(record.states[idx])
          : next
      }
      return [record.states[idx], setState]
    },
    useReducer: (reducer: (state: unknown, action: unknown) => unknown, initial: unknown) => {
      const record = activeRecord!
      const idx = stateIndex++
      if (!(idx in record.states)) record.states[idx] = initial
      const dispatch = (action: unknown) => {
        record.states[idx] = reducer(record.states[idx], action)
      }
      return [record.states[idx], dispatch]
    },
    useRef: (initial: unknown) => {
      const record = activeRecord!
      const idx = refIndex++
      if (!(idx in record.refs)) record.refs[idx] = { current: initial }
      return record.refs[idx]
    },
    useCallback: <T extends (...args: never[]) => unknown>(fn: T) => fn,
    useEffect: (cb: () => void | (() => void), deps?: unknown[]) => {
      const record = activeRecord!
      const idx = effectIndex++
      const prev = record.effects[idx]
      if (!prev || depsChanged(prev.deps, deps)) {
        record.effects[idx] = { deps, cb, cleanup: prev?.cleanup }
        record.pendingEffects.push(idx)
      }
    },
  }
})

import { defaultSettings, shellStyleForSettings } from '../lib/appearance'
import { useAdminController } from './useAdminController'
import { loadStoredPublicSettings, normalizeStoredPublicSettings, publicSettingsCacheKey, usePublicSettings } from './usePublicSettings'

function createHookRunner<T>(renderFn: () => T) {
  const record: HookRecord = { states: [], refs: [], effects: [], pendingEffects: [] }
  const render = (): T => {
    activeRecord = record
    stateIndex = 0
    refIndex = 0
    effectIndex = 0
    const result = renderFn()
    activeRecord = null
    while (record.pendingEffects.length > 0) {
      const idx = record.pendingEffects.shift()!
      const effect = record.effects[idx]
      effect.cleanup?.()
      const cleanup = effect.cb()
      effect.cleanup = typeof cleanup === 'function' ? cleanup : undefined
    }
    return result
  }
  const unmount = () => {
    for (const effect of record.effects) effect.cleanup?.()
  }
  return { render, unmount }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((res) => { resolve = res })
  return { promise, resolve }
}

function installMemoryWindow() {
  const store = new Map<string, string>()
  const storage = {
    getItem: (key: string) => (store.has(key) ? store.get(key)! : null),
    setItem: (key: string, value: string) => { store.set(key, String(value)) },
    removeItem: (key: string) => { store.delete(key) },
  }
  ;(globalThis as { window?: unknown }).window = {
    localStorage: storage,
    sessionStorage: storage,
    location: { protocol: 'https:', host: 'zeno.example', origin: 'https://zeno.example' },
  }
  return store
}

beforeEach(() => {
  installMemoryWindow()
})

afterEach(() => {
  vi.restoreAllMocks()
  delete (globalThis as { window?: unknown }).window
})

describe('normalizeStoredPublicSettings', () => {
  it('fills missing legacy fields from defaults without crashing appearance styles', () => {
    const normalized = normalizeStoredPublicSettings({
      siteTitle: 'Legacy Zeno',
      themeColor: '#0071e3',
      theme: 'dark',
    })
    expect(normalized).not.toBeNull()
    expect(normalized?.siteTitle).toBe('Legacy Zeno')
    expect(normalized?.mobileBackgroundUrl).toBe('')
    expect(normalized?.cardOpacity).toBe(defaultSettings.cardOpacity)
    expect(() => shellStyleForSettings(normalized!)).not.toThrow()
  })

  it('sanitizes explicit null and invalid field types so stale cache never crashes render', () => {
    const normalized = normalizeStoredPublicSettings({
      siteTitle: 'Null Safe Zeno',
      themeColor: '#2563eb',
      logoUrl: null,
      backgroundUrl: null,
      desktopBackgroundUrl: null,
      mobileBackgroundUrl: null,
      customCode: null,
      cardOpacity: null,
      cardBlur: '20',
      revision: -5,
    })
    expect(normalized).not.toBeNull()
    expect(normalized?.mobileBackgroundUrl).toBe('')
    expect(normalized?.desktopBackgroundUrl).toBe('')
    expect(normalized?.logoUrl).toBe(defaultSettings.logoUrl)
    expect(normalized?.cardOpacity).toBe(defaultSettings.cardOpacity)
    expect(normalized?.cardBlur).toBe(defaultSettings.cardBlur)
    expect(normalized?.revision).toBe(0)
    expect(() => shellStyleForSettings(normalized!)).not.toThrow()
  })

  it('returns null for non-object or malformed stored payloads', () => {
    expect(normalizeStoredPublicSettings(null)).toBeNull()
    expect(normalizeStoredPublicSettings(['bad'])).toBeNull()
    expect(normalizeStoredPublicSettings({ siteTitle: 123, themeColor: '#2563eb' })).toBeNull()
    window.localStorage.setItem(publicSettingsCacheKey, '{invalid')
    expect(loadStoredPublicSettings()).toBeNull()
  })
})

describe('usePublicSettings coordination with useAdminController', () => {
  it('applies the initial public GET even if an unauthenticated admin surface mounts while GET is in flight', async () => {
    const publicGet = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockImplementation((input) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
      if (url.includes('/api/public/v1/settings')) return publicGet.promise
      return Promise.resolve(new Response(JSON.stringify({ error: 'unauthorized' }), { status: 401 }))
    })

    const pubRunner = createHookRunner(() => usePublicSettings())
    const initialPub = pubRunner.render()
    const adminRunner = createHookRunner(() => useAdminController(true, {
      initialSettings: initialPub.settings,
      onSettingsChange: initialPub.setSettings,
    }))
    adminRunner.render()

    publicGet.resolve(new Response(JSON.stringify({
      site_title: 'Production Zeno',
      logo_url: '/assets/logo/id.png',
      theme: 'dark',
      background_url: '',
      desktop_background_url: '',
      mobile_background_url: '',
      appearance_preset: 'gaussian_blur',
      server_card_theme: 'classic',
      theme_color: '#0071e3',
      revision: 7,
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise((resolve) => setTimeout(resolve, 0))

    const updatedPub = pubRunner.render()
    expect(updatedPub.settingsReady).toBe(true)
    expect(updatedPub.settings.siteTitle).toBe('Production Zeno')
    expect(updatedPub.settings.revision).toBe(7)
    adminRunner.unmount()
    pubRunner.unmount()
  })

  it('rejects a late initial GET so it never overwrites newer saved settings', async () => {
    const publicGet = deferred<Response>()
    vi.spyOn(globalThis, 'fetch').mockImplementation(() => publicGet.promise)

    const pubRunner = createHookRunner(() => usePublicSettings())
    const first = pubRunner.render()
    first.setSettings({ ...defaultSettings, siteTitle: 'Saved Title', revision: 9 })

    publicGet.resolve(new Response(JSON.stringify({
      site_title: 'Stale Title',
      logo_url: '/assets/logo/id.png',
      theme: 'system',
      background_url: '',
      desktop_background_url: '',
      mobile_background_url: '',
      appearance_preset: 'default',
      server_card_theme: 'classic',
      theme_color: '#2563eb',
      revision: 3,
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await new Promise((resolve) => setTimeout(resolve, 0))

    const after = pubRunner.render()
    expect(after.settings.siteTitle).toBe('Saved Title')
    expect(after.settings.revision).toBe(9)
    pubRunner.unmount()
  })
})
