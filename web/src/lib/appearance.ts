import { type CSSProperties, useLayoutEffect } from 'react'
import type { AdminSettings, AdminTheme, AppearancePreset } from '../types'

export type AppearanceValues = Pick<AdminSettings, 'appearancePreset' | 'cardOpacity' | 'cardBlur' | 'cardRadius' | 'borderStrength' | 'shadowStrength' | 'backgroundOverlay' | 'themeColor'>

const defaultAppearancePreset: AppearanceValues = {
  appearancePreset: 'default',
  cardOpacity: 0.7,
  cardBlur: 0,
  cardRadius: 20,
  borderStrength: 0.3,
  shadowStrength: 0.2,
  backgroundOverlay: 0,
  themeColor: '#2563eb',
}

export const defaultSettings: AdminSettings = {
  siteTitle: 'Zeno',
  logoUrl: '/assets/logo/id.png',
  theme: 'system',
  agentControllerUrl: '',
  backgroundUrl: '',
  desktopBackgroundUrl: '',
  mobileBackgroundUrl: '',
  serverCardTheme: 'classic',
  ...defaultAppearancePreset,
  customCode: '',
  revision: 0,
}

export const appearancePresets: Record<AppearancePreset, AppearanceValues> = {
  default: defaultAppearancePreset,
  gaussian_blur: {
    ...defaultAppearancePreset,
    appearancePreset: 'gaussian_blur',
    cardOpacity: 0.5,
    cardBlur: 15,
    borderStrength: 0.3,
    shadowStrength: 0.3,
    backgroundOverlay: 0.05,
  },
}

export const appearancePresetOptions: Array<{ value: AppearancePreset; label: string }> = [
  { value: 'default', label: '默认主题' },
  { value: 'gaussian_blur', label: '高斯模糊主题' },
]

function backgroundImageValue(url: string): string {
  return `url("${url.replaceAll('"', '%22')}")`
}

function clampNumber(value: number, min: number, max: number): number {
  return Number.isFinite(value) ? Math.min(max, Math.max(min, value)) : min
}

export function appearanceValuesForSettings(settings: AdminSettings): AppearanceValues {
  const preset = settings.appearancePreset === 'gaussian_blur' ? 'gaussian_blur' : 'default'
  const fallback = appearancePresets[preset]
  return {
    appearancePreset: preset,
    cardOpacity: clampNumber(settings.cardOpacity ?? fallback.cardOpacity, 0.2, 1),
    cardBlur: clampNumber(settings.cardBlur ?? fallback.cardBlur, 0, 40),
    cardRadius: clampNumber(settings.cardRadius ?? fallback.cardRadius, 8, 36),
    borderStrength: clampNumber(settings.borderStrength ?? fallback.borderStrength, 0, 1),
    shadowStrength: clampNumber(settings.shadowStrength ?? fallback.shadowStrength, 0, 1),
    backgroundOverlay: clampNumber(settings.backgroundOverlay ?? fallback.backgroundOverlay, 0, 0.8),
    themeColor: /^#[0-9a-fA-F]{6}$/.test(settings.themeColor ?? '') ? settings.themeColor : fallback.themeColor,
  }
}

function hexToRgb(value: string): { r: number; g: number; b: number } {
  const normalized = /^#[0-9a-fA-F]{6}$/.test(value) ? value.slice(1) : '2563eb'
  return {
    r: Number.parseInt(normalized.slice(0, 2), 16),
    g: Number.parseInt(normalized.slice(2, 4), 16),
    b: Number.parseInt(normalized.slice(4, 6), 16),
  }
}

function rgbaFromHex(value: string, alpha: number): string {
  const { r, g, b } = hexToRgb(value)
  return `rgba(${r}, ${g}, ${b}, ${alpha.toFixed(3)})`
}

export function storedThemeOverride(): AdminTheme | null {
  if (typeof window === 'undefined') return null
  const value = window.localStorage.getItem('zeno_theme_override')
  return value === 'system' || value === 'light' || value === 'dark' ? value : null
}

export function storedBackgroundEnabled(): boolean {
  if (typeof window === 'undefined') return false
  return window.localStorage.getItem('zeno_background_enabled') === 'true'
}

function systemTheme(): Exclude<AdminTheme, 'system'> {
  if (typeof window !== 'undefined' && window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark'
  return 'light'
}

export function resolvedTheme(theme: AdminTheme): Exclude<AdminTheme, 'system'> {
  return theme === 'system' ? systemTheme() : theme
}

export function settingsForChrome(settings: AdminSettings, themeOverride: AdminTheme | null, backgroundEnabled: boolean): AdminSettings {
  const nextSettings = { ...settings, theme: themeOverride ?? settings.theme }
  if (backgroundEnabled) return nextSettings
  return { ...nextSettings, backgroundUrl: '', desktopBackgroundUrl: '', mobileBackgroundUrl: '' }
}

export function shellStyleForSettings(settings: AdminSettings): CSSProperties | undefined {
  const desktopBackgroundUrl = (settings.desktopBackgroundUrl || settings.backgroundUrl).trim()
  const mobileBackgroundUrl = settings.mobileBackgroundUrl.trim()
  const hasDedicatedMobileBackground = mobileBackgroundUrl !== ''
  const hasBackgroundImage = desktopBackgroundUrl !== '' || mobileBackgroundUrl !== ''
  const appearance = appearanceValuesForSettings(settings)
  const resolved = resolvedTheme(settings.theme)
  const themeColor = appearance.themeColor
  const themeRgb = hexToRgb(themeColor)
  const cardOpacity = appearance.cardOpacity
  const highContrastGaussian = resolved === 'dark' && appearance.appearancePreset === 'gaussian_blur'
  const foreground = resolved === 'dark' ? '#f8fafc' : '#0f172a'
  const muted = highContrastGaussian ? '#cbd5e1' : resolved === 'dark' ? '#94a3b8' : '#53657d'
  const surfaceBase = resolved === 'dark' ? '15, 23, 42' : '255, 255, 255'
  const shadowBase = resolved === 'dark' ? '0, 0, 0' : '15, 23, 42'
  const shadowAlpha = 0.04 + appearance.shadowStrength * (resolved === 'dark' ? 0.44 : 0.22)
  const backgroundOverlayBase = resolved === 'dark' ? '0, 0, 0' : '255, 255, 255'
  const pageSurface = hasBackgroundImage ? `rgba(${surfaceBase}, ${cardOpacity.toFixed(3)})` : `rgb(${surfaceBase})`
  const gaussianOverlay = appearance.appearancePreset === 'gaussian_blur'
  const overlayOpacity = Math.min(0.84, Math.max(0.62, cardOpacity + (gaussianOverlay ? 0.14 : 0.1)))
  const overlayFilter = appearance.cardBlur > 0 ? `blur(${appearance.cardBlur}px) saturate(1.08)` : 'none'
  return {
    '--zeno-desktop-background-image': desktopBackgroundUrl === '' ? 'none' : backgroundImageValue(desktopBackgroundUrl),
    '--zeno-mobile-background-image': hasDedicatedMobileBackground ? backgroundImageValue(mobileBackgroundUrl) : (desktopBackgroundUrl === '' ? 'none' : backgroundImageValue(desktopBackgroundUrl)),
    '--blue': themeColor,
    '--foreground': foreground,
    '--muted': muted,
    '--border': rgbaFromHex(themeColor, appearance.borderStrength),
    '--metric-shadow': rgbaFromHex(themeColor, Math.max(0.06, appearance.shadowStrength * 0.22)),
    '--page-surface': pageSurface,
    '--admin-secondary-surface': `rgb(${surfaceBase})`,
    '--surface-strong': 'transparent',
    '--surface': 'transparent',
    '--surface-soft': 'transparent',
    '--secondary': 'transparent',
    '--metric-bg': 'transparent',
    '--field-bg': 'transparent',
    '--control-bg': 'transparent',
    '--usage-track-bg': resolved === 'dark' ? 'rgba(226, 232, 240, 0.17)' : 'rgba(148, 163, 184, 0.12)',
    '--usage-track-border': highContrastGaussian ? 'rgba(203, 213, 225, 0.24)' : resolved === 'dark' ? 'rgba(148, 163, 184, 0.14)' : 'rgba(148, 163, 184, 0.12)',
    '--zeno-overlay-surface': `rgba(${surfaceBase}, ${overlayOpacity.toFixed(3)})`,
    '--zeno-overlay-filter': overlayFilter,
    '--radius-panel': `${appearance.cardRadius}px`,
    '--radius-card': `${Math.max(10, appearance.cardRadius - 4)}px`,
    '--radius-field': `${Math.max(8, appearance.cardRadius - 8)}px`,
    '--zeno-card-blur': `${appearance.cardBlur}px`,
    '--zeno-card-highlight': resolved === 'dark' ? `rgba(255, 255, 255, ${Math.min(0.18, 0.04 + appearance.shadowStrength * 0.12).toFixed(3)})` : `rgba(255, 255, 255, ${Math.min(0.9, 0.28 + cardOpacity * 0.42).toFixed(3)})`,
    '--zeno-card-shadow': `0 10px 26px -24px rgba(${shadowBase}, ${shadowAlpha.toFixed(3)}), 0 1px 2px rgba(${shadowBase}, ${(0.02 + appearance.shadowStrength * 0.05).toFixed(3)})`,
    '--zeno-background-overlay-color': `rgba(${backgroundOverlayBase}, ${appearance.backgroundOverlay.toFixed(3)})`,
    '--zeno-theme-rgb': `${themeRgb.r}, ${themeRgb.g}, ${themeRgb.b}`,
    backgroundSize: 'cover',
    backgroundAttachment: 'fixed',
  } as CSSProperties
}

const documentThemeVariableNames = [
  '--blue',
  '--foreground',
  '--muted',
  '--border',
  '--metric-shadow',
  '--page-surface',
  '--admin-secondary-surface',
  '--surface-strong',
  '--surface',
  '--surface-soft',
  '--secondary',
  '--metric-bg',
  '--field-bg',
  '--control-bg',
  '--usage-track-bg',
  '--usage-track-border',
  '--zeno-overlay-surface',
  '--zeno-overlay-filter',
  '--radius-panel',
  '--radius-card',
  '--radius-field',
  '--zeno-card-blur',
  '--zeno-card-highlight',
  '--zeno-card-shadow',
  '--zeno-theme-rgb',
] as const

export function useDocumentTheme(settings: AdminSettings) {
  useLayoutEffect(() => {
    if (typeof document === 'undefined') return undefined
    const root = document.documentElement
    const previousTheme = root.getAttribute('data-zeno-theme')
    const previousValues = new Map(documentThemeVariableNames.map((name) => [name, root.style.getPropertyValue(name)]))
    const media = window.matchMedia?.('(prefers-color-scheme: dark)')
    const apply = () => {
      const theme = resolvedTheme(settings.theme)
      const themeStyle = shellStyleForSettings(settings) as Record<string, string | number> | undefined
      root.dataset.zenoTheme = theme
      for (const name of documentThemeVariableNames) {
        const value = themeStyle?.[name]
        if (value === undefined || value === null || value === '') root.style.removeProperty(name)
        else root.style.setProperty(name, String(value))
      }
    }
    apply()
    if (settings.theme === 'system') media?.addEventListener?.('change', apply)
    return () => {
      if (settings.theme === 'system') media?.removeEventListener?.('change', apply)
      if (previousTheme === null) root.removeAttribute('data-zeno-theme')
      else root.setAttribute('data-zeno-theme', previousTheme)
      for (const [name, value] of previousValues) {
        if (value === '') root.style.removeProperty(name)
        else root.style.setProperty(name, value)
      }
    }
  }, [
    settings.theme,
    settings.backgroundUrl,
    settings.desktopBackgroundUrl,
    settings.mobileBackgroundUrl,
    settings.appearancePreset,
    settings.cardOpacity,
    settings.cardBlur,
    settings.cardRadius,
    settings.borderStrength,
    settings.shadowStrength,
    settings.backgroundOverlay,
    settings.themeColor,
  ])
}

export function documentBrandingForSettings(settings: AdminSettings) {
  const siteTitle = (settings.siteTitle || defaultSettings.siteTitle).trim() || defaultSettings.siteTitle
  const logoUrl = (settings.logoUrl || defaultSettings.logoUrl).trim() || defaultSettings.logoUrl
  return { title: siteTitle, iconHref: logoUrl }
}

export const themeOptions: Array<{ value: AdminTheme; label: string }> = [
  { value: 'system', label: '跟随系统' },
  { value: 'light', label: '浅色' },
  { value: 'dark', label: '深色' },
]


export function applyDocumentBranding(settings: AdminSettings) {
  if (typeof document === 'undefined') return
  const branding = documentBrandingForSettings(settings)
  document.title = branding.title
  let icon = document.head.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (!icon) {
    icon = document.createElement('link')
    icon.rel = 'icon'
    document.head.appendChild(icon)
  }
  icon.href = branding.iconHref
}
