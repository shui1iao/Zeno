import { useCallback, useEffect, useRef, useState, type SetStateAction } from 'react'
import { fetchPublicSettings } from '../api/publicClient'
import { defaultSettings } from '../lib/appearance'
import type { AdminSettings } from '../types'

export const publicSettingsCacheKey = 'zeno_public_settings_v1'

function stringOrDefault(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback
}

function finiteNumberOrDefault(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : fallback
}

export function normalizeStoredPublicSettings(parsed: unknown): AdminSettings | null {
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return null
  const record = parsed as Record<string, unknown>
  if (typeof record.siteTitle !== 'string' || typeof record.themeColor !== 'string') return null
  const desktopBackgroundUrl = typeof record.desktopBackgroundUrl === 'string'
    ? record.desktopBackgroundUrl
    : stringOrDefault(record.backgroundUrl, defaultSettings.desktopBackgroundUrl)
  const revision = typeof record.revision === 'number' && Number.isSafeInteger(record.revision) && record.revision >= 0
    ? record.revision
    : defaultSettings.revision
  return {
    siteTitle: record.siteTitle,
    logoUrl: stringOrDefault(record.logoUrl, defaultSettings.logoUrl),
    theme: record.theme === 'light' || record.theme === 'dark' ? record.theme : 'system',
    agentControllerUrl: stringOrDefault(record.agentControllerUrl, defaultSettings.agentControllerUrl),
    backgroundUrl: stringOrDefault(record.backgroundUrl, desktopBackgroundUrl),
    desktopBackgroundUrl,
    mobileBackgroundUrl: stringOrDefault(record.mobileBackgroundUrl, defaultSettings.mobileBackgroundUrl),
    appearancePreset: record.appearancePreset === 'gaussian_blur' ? 'gaussian_blur' : 'default',
    serverCardTheme: record.serverCardTheme === 'capsule' ? 'capsule' : 'classic',
    cardOpacity: finiteNumberOrDefault(record.cardOpacity, defaultSettings.cardOpacity),
    cardBlur: finiteNumberOrDefault(record.cardBlur, defaultSettings.cardBlur),
    cardRadius: finiteNumberOrDefault(record.cardRadius, defaultSettings.cardRadius),
    borderStrength: finiteNumberOrDefault(record.borderStrength, defaultSettings.borderStrength),
    shadowStrength: finiteNumberOrDefault(record.shadowStrength, defaultSettings.shadowStrength),
    backgroundOverlay: finiteNumberOrDefault(record.backgroundOverlay, defaultSettings.backgroundOverlay),
    themeColor: record.themeColor,
    customCode: stringOrDefault(record.customCode, defaultSettings.customCode),
    revision,
    updatedAt: typeof record.updatedAt === 'string' ? record.updatedAt : undefined,
  }
}

export function loadStoredPublicSettings(): AdminSettings | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem(publicSettingsCacheKey)
    if (!raw) return null
    return normalizeStoredPublicSettings(JSON.parse(raw))
  } catch {
    return null
  }
}

export function rememberPublicSettings(settings: AdminSettings): void {
  if (typeof window === 'undefined') return
  try {
    window.localStorage.setItem(publicSettingsCacheKey, JSON.stringify(settings))
  } catch {}
}

export function usePublicSettings() {
  const cached = loadStoredPublicSettings()
  const [settings, setSettingsState] = useState<AdminSettings>(() => cached ?? defaultSettings)
  const [settingsReady, setSettingsReady] = useState(() => cached !== null)
  const writeEpochRef = useRef(0)

  const setSettings = useCallback((action: SetStateAction<AdminSettings>) => {
    writeEpochRef.current += 1
    setSettingsState((current) => {
      const next = typeof action === 'function' ? action(current) : action
      rememberPublicSettings(next)
      return next
    })
  }, [])

  useEffect(() => {
    let cancelled = false
    const requestEpoch = writeEpochRef.current
    fetchPublicSettings()
      .then((loadedSettings) => {
        if (cancelled || writeEpochRef.current !== requestEpoch) return
        rememberPublicSettings(loadedSettings)
        setSettingsState(loadedSettings)
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setSettingsReady(true)
      })
    return () => { cancelled = true }
  }, [])

  return { settings, settingsReady, setSettings }
}
