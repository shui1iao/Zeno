import { type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { defaultSettings, fallbackLogoUrl, resolvedTheme, themeOptions } from '../lib/appearance'
import type { AdminSettings, AdminTheme } from '../types'

export interface DashboardHeaderProps {
  settings?: AdminSettings
  onHome: () => void
  onAdmin: () => void
  onAdminIntent?: () => void
  adminLabel?: string
  leadingAction?: ReactNode
  trailingAction?: ReactNode
  onThemeChange?: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

interface ThemeMenuPosition {
  top: number
  left: number
}

const themeMenuWidth = 128
const themeMenuMargin = 8

function resolveThemeMenuPosition(button: HTMLButtonElement, viewportWidth: number): ThemeMenuPosition {
  const rect = button.getBoundingClientRect()
  return {
    top: rect.bottom + themeMenuMargin,
    left: Math.max(themeMenuMargin, Math.min(viewportWidth - themeMenuWidth - themeMenuMargin, rect.right - themeMenuWidth)),
  }
}

function BrandLogo({ logoUrl, siteTitle }: { logoUrl?: string; siteTitle?: string }) {
  const source = (logoUrl ?? '').trim()
  const [currentSource, setCurrentSource] = useState(source)
  const [showLetterFallback, setShowLetterFallback] = useState(source === '')

  useEffect(() => {
    setCurrentSource(source)
    setShowLetterFallback(source === '')
  }, [source])

  if (showLetterFallback) {
    return <span className="brand-logo-fallback" role="img" aria-label={`${siteTitle || 'Zeno'} logo`}>Z</span>
  }

  return (
    <img
      src={currentSource}
      width="32"
      height="32"
      decoding="async"
      alt={`${siteTitle || 'Zeno'} logo`}
      onError={() => {
        if (currentSource !== defaultSettings.logoUrl) setCurrentSource(defaultSettings.logoUrl)
        else if (currentSource !== fallbackLogoUrl) setCurrentSource(fallbackLogoUrl)
        else setShowLetterFallback(true)
      }}
    />
  )
}

export function DashboardHeader({ settings = defaultSettings, onHome, onAdmin, onAdminIntent, adminLabel = '后台', leadingAction, trailingAction, onThemeChange, onBackgroundToggle, backgroundEnabled = false }: DashboardHeaderProps) {
  const [themeMenuOpen, setThemeMenuOpen] = useState(false)
  const [themeMenuPosition, setThemeMenuPosition] = useState<ThemeMenuPosition | null>(null)
  const themeMenuRef = useRef<HTMLDivElement>(null)
  const themeButtonRef = useRef<HTMLButtonElement>(null)
  const themePopoverRef = useRef<HTMLDivElement>(null)
  const themeMode = settings.theme
  const currentTheme = resolvedTheme(themeMode)
  const currentThemeLabel = themeOptions.find((option) => option.value === themeMode)?.label ?? '跟随系统'
  const backgroundControlLabel = onBackgroundToggle
    ? (backgroundEnabled ? '关闭背景图' : '开启背景图')
    : (backgroundEnabled ? '背景图加载中' : '背景图未配置')

  const closeThemeMenu = useCallback((restoreFocus = false) => {
    setThemeMenuOpen(false)
    setThemeMenuPosition(null)
    if (restoreFocus && typeof window !== 'undefined') {
      window.requestAnimationFrame(() => themeButtonRef.current?.focus())
    }
  }, [])

  useEffect(() => {
    if (!themeMenuOpen || typeof window === 'undefined') return undefined
    const handlePointerDown = (event: PointerEvent) => {
      if (themeMenuRef.current?.contains(event.target as Node)) return
      if (themePopoverRef.current?.contains(event.target as Node)) return
      closeThemeMenu()
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeThemeMenu(true)
    }
    const updatePosition = () => {
      const button = themeButtonRef.current
      if (!button) return
      setThemeMenuPosition(resolveThemeMenuPosition(button, window.innerWidth))
    }
    updatePosition()
    window.addEventListener('pointerdown', handlePointerDown)
    window.addEventListener('keydown', handleKeyDown)
    window.addEventListener('resize', updatePosition)
    window.addEventListener('scroll', updatePosition, true)
    return () => {
      window.removeEventListener('pointerdown', handlePointerDown)
      window.removeEventListener('keydown', handleKeyDown)
      window.removeEventListener('resize', updatePosition)
      window.removeEventListener('scroll', updatePosition, true)
    }
  }, [closeThemeMenu, themeMenuOpen])

  const selectTheme = (nextTheme: AdminTheme) => {
    onThemeChange?.(nextTheme)
    closeThemeMenu(true)
  }

  const toggleThemeMenu = () => {
    if (themeMenuOpen) {
      closeThemeMenu()
      return
    }
    const button = themeButtonRef.current
    if (button) setThemeMenuPosition(resolveThemeMenuPosition(button, window.innerWidth))
    setThemeMenuOpen(true)
  }

  return (
    <header className="kulin-nav">
      <button className="brand" type="button" onClick={onHome}>
        <span className="brand-logo"><BrandLogo logoUrl={settings.logoUrl} siteTitle={settings.siteTitle} /></span>
        <span>{settings.siteTitle || 'Zeno'}</span>
      </button>
      <nav className="nav-actions" aria-label="dashboard actions">
        {leadingAction}
        <div className="theme-menu" ref={themeMenuRef}>
          <button ref={themeButtonRef} className="nav-icon-button" type="button" aria-label={`主题：${currentThemeLabel}`} aria-haspopup="menu" aria-expanded={themeMenuOpen} onClick={toggleThemeMenu}>{themeMode === 'system' ? <MonitorIcon /> : currentTheme === 'dark' ? <MoonIcon /> : <SunIcon />}<span className="sr-only">切换深浅色</span></button>
        </div>
        <button className={`nav-icon-button${backgroundEnabled ? ' is-solid' : ''}`} type="button" aria-label={backgroundControlLabel} aria-pressed={backgroundEnabled} disabled={!onBackgroundToggle} onClick={onBackgroundToggle}><ImageMinusIcon /><span className="sr-only">开关背景图</span></button>
        <button className="login-link" type="button" onPointerEnter={onAdminIntent} onPointerDown={onAdminIntent} onFocus={onAdminIntent} onClick={onAdmin}>{adminLabel}</button>
        {trailingAction}
      </nav>
      {themeMenuOpen && themeMenuPosition && typeof document !== 'undefined' && createPortal(
        <div ref={themePopoverRef} className={`theme-menu-popover${settings.appearancePreset === 'gaussian_blur' && backgroundEnabled ? ' is-gaussian' : ''}`} role="menu" style={themeMenuPosition}>
          {themeOptions.map((option) => (
            <button key={option.value} type="button" role="menuitemradio" aria-checked={themeMode === option.value} data-active={themeMode === option.value} onClick={() => selectTheme(option.value)}>
              <span>{option.label}</span>
            </button>
          ))}
        </div>,
        document.body,
      )}
    </header>
  )
}

function SunIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
    </svg>
  )
}

function MoonIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M20.99 12.58A8.5 8.5 0 1 1 11.42 3a6.6 6.6 0 0 0 9.57 9.57Z" />
    </svg>
  )
}

function MonitorIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="4" width="18" height="12" rx="2" />
      <path d="M8 20h8M12 16v4" />
    </svg>
  )
}

function ImageMinusIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M21 9v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h7" />
      <path d="M16 5h6" />
      <circle cx="9" cy="9" r="2" />
      <path d="m21 15-3.086-3.086a2 2 0 0 0-2.828 0L6 21" />
    </svg>
  )
}
